package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "deploytar/proto/deploytar/proto/fileservice/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func setupTestGRPCServer(t *testing.T) (pb.FileServiceClient, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	serverInstance := NewGRPCListDirectoryServer()
	s := grpc.NewServer()
	pb.RegisterFileServiceServer(s, serverInstance)

	go func() {
		if errS := s.Serve(lis); errS != nil && !strings.Contains(errS.Error(), "use of closed network connection") {
			t.Logf("gRPC server Serve error: %v", errS)
		}
	}()

	_, cancelConn := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelConn()

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err, "Failed to connect to gRPC server at %s. Ensure server goroutine started.", lis.Addr().String())

	connCtx2, cancelConn2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelConn2()

	client := pb.NewFileServiceClient(conn)

	for {
		rootDir := "/"
		_, err := client.ListDirectory(connCtx2, &pb.ListDirectoryRequest{Directory: &rootDir})
		if err == nil {
			break
		}
		if connCtx2.Err() != nil {
			t.Fatal("Failed to establish connection within timeout")
		}
		time.Sleep(100 * time.Millisecond)
	}

	cleanup := func() {
		if err := conn.Close(); err != nil {
			t.Logf("Error closing client connection: %v", err)
		}
		if err := lis.Close(); err != nil {
			t.Logf("Error closing listener: %v", err)
		}
	}

	return client, cleanup
}

func createTestTarArchive(t *testing.T, dir, archiveName string, files map[string]string) string {
	t.Helper()
	archivePath := filepath.Join(dir, archiveName)
	outFile, err := os.Create(archivePath)
	require.NoError(t, err)
	defer func() {
		if closeErr := outFile.Close(); closeErr != nil {
			t.Logf("Failed to close output file: %v", closeErr)
		}
	}()

	tw := tar.NewWriter(outFile)
	defer func() {
		if closeErr := tw.Close(); closeErr != nil {
			t.Logf("Failed to close tar writer: %v", closeErr)
		}
	}()

	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0600,
			Size: int64(len(content)),
		}
		err = tw.WriteHeader(hdr)
		require.NoError(t, err)
		_, err = tw.Write([]byte(content))
		require.NoError(t, err)
	}
	return archivePath
}

func createTestTgzArchive(t *testing.T, dir, archiveName string, files map[string]string) string {
	t.Helper()
	archivePath := filepath.Join(dir, archiveName)
	outFile, err := os.Create(archivePath)
	require.NoError(t, err)
	defer func() {
		if closeErr := outFile.Close(); closeErr != nil {
			t.Logf("Failed to close output file: %v", closeErr)
		}
	}()

	gzw := gzip.NewWriter(outFile)
	defer func() {
		if closeErr := gzw.Close(); closeErr != nil {
			t.Logf("Failed to close gzip writer: %v", closeErr)
		}
	}()

	tw := tar.NewWriter(gzw)
	defer func() {
		if closeErr := tw.Close(); closeErr != nil {
			t.Logf("Failed to close tar writer: %v", closeErr)
		}
	}()

	for name, content := range files {
		hdr := &tar.Header{
			Name:    name,
			Mode:    0600,
			Size:    int64(len(content)),
			ModTime: time.Now(),
		}
		err = tw.WriteHeader(hdr)
		require.NoError(t, err)
		_, err = tw.Write([]byte(content))
		require.NoError(t, err)
	}
	return archivePath
}

func createTestGzFile(t *testing.T, dir, gzFilename, originalFilename, content string) string {
	t.Helper()
	gzPath := filepath.Join(dir, gzFilename)
	outFile, err := os.Create(gzPath)
	require.NoError(t, err)
	defer func() {
		if closeErr := outFile.Close(); closeErr != nil {
			t.Logf("Failed to close output file: %v", closeErr)
		}
	}()

	gzw := gzip.NewWriter(outFile)
	defer func() {
		if closeErr := gzw.Close(); closeErr != nil {
			t.Logf("Failed to close gzip writer: %v", closeErr)
		}
	}()

	_, err = gzw.Write([]byte(content))
	require.NoError(t, err)
	return gzPath
}

func sendFileAsStream(t *testing.T, client pb.FileServiceClient, targetPath, sourceFilename string, fileContent []byte) (*pb.UploadFileResponse, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := client.UploadFile(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to create upload stream: %v", err)
	}

	fileInfo := &pb.FileInfo{
		Path:     &targetPath,
		Filename: &sourceFilename,
	}
	req := &pb.UploadFileRequest{
		Data: &pb.UploadFileRequest_Info{Info: fileInfo},
	}
	if err = stream.Send(req); err != nil {
		_, recvErr := stream.CloseAndRecv()
		if recvErr != nil {
			return nil, status.Errorf(codes.Internal, "Failed to send file info (send err: %v). Server error on CloseAndRecv: %v", err, recvErr)
		}
		return nil, status.Errorf(codes.Internal, "Failed to send file info: %v. Server did not return specific error on CloseAndRecv.", err)
	}

	reader := bytes.NewReader(fileContent)
	buffer := make([]byte, 1024)

	for {
		n, readErr := reader.Read(buffer)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_, recvErr := stream.CloseAndRecv()
			if recvErr != nil {
				return nil, status.Errorf(codes.Internal, "Failed to read chunk from source (readErr: %v). Server error on CloseAndRecv: %v", readErr, recvErr)
			}
			return nil, status.Errorf(codes.Internal, "Failed to read chunk from source: %v", readErr)
		}

		chunkReq := &pb.UploadFileRequest{
			Data: &pb.UploadFileRequest_ChunkData{ChunkData: buffer[:n]},
		}
		if err = stream.Send(chunkReq); err != nil {
			_, recvErr := stream.CloseAndRecv()
			if recvErr != nil {
				return nil, status.Errorf(codes.Internal, "Failed to send chunk data (send err: %v). Server error on CloseAndRecv: %v", err, recvErr)
			}
			return nil, status.Errorf(codes.Internal, "Failed to send chunk data: %v", err)
		}
	}

	return stream.CloseAndRecv()
}

func TestUploadFile_NormalFileUpload(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempUploadDir := t.TempDir()
	fileName := "test.txt"
	fileContent := "Hello, gRPC upload!"
	targetDir := filepath.Join(tempUploadDir, "normal_upload_dest")

	resp, err := sendFileAsStream(t, client, targetDir, fileName, []byte(fileContent))
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	expectedFilePath := filepath.Join(targetDir, fileName)
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedFilePath), filepath.Clean(*resp.FilePath))

	uploadedFileContent, err := os.ReadFile(expectedFilePath)
	require.NoError(t, err)
	assert.Equal(t, fileContent, string(uploadedFileContent))

	_, err = os.Stat(targetDir)
	assert.NoError(t, err, "Target directory should have been created by the handler.")
}

func TestUploadFile_TarArchive(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempSourceDir := t.TempDir()
	tempUploadDir := t.TempDir()

	archiveName := "myarchive.tar"
	filesInArchive := map[string]string{
		"file1.txt":            "Tar content 1",
		"dir_in_tar/file2.txt": "Tar content 2",
	}
	tarFilePath := createTestTarArchive(t, tempSourceDir, archiveName, filesInArchive)
	tarFileContent, err := os.ReadFile(tarFilePath)
	require.NoError(t, err)

	targetExtractDir := filepath.Join(tempUploadDir, "tar_extraction_point")

	resp, err := sendFileAsStream(t, client, targetExtractDir, archiveName, tarFileContent)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(targetExtractDir), filepath.Clean(*resp.FilePath), "Response FilePath should be the extraction directory for archives.")

	for name, expectedContent := range filesInArchive {
		extractedFilePath := filepath.Join(targetExtractDir, name)
		content, errRead := os.ReadFile(extractedFilePath)
		require.NoError(t, errRead, "Failed to read extracted file: %s", extractedFilePath)
		assert.Equal(t, expectedContent, string(content))
	}
	_, err = os.Stat(targetExtractDir)
	assert.NoError(t, err, "Target extraction directory should exist.")
}

func TestUploadFile_TgzArchive(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempSourceDir := t.TempDir()
	tempUploadDir := t.TempDir()

	archiveName := "archive.tgz"
	filesInArchive := map[string]string{
		"doc.txt":          "TGZ content A",
		"data/config.json": `{"key":"value"}`,
	}
	tgzFilePath := createTestTgzArchive(t, tempSourceDir, archiveName, filesInArchive)
	tgzFileContent, err := os.ReadFile(tgzFilePath)
	require.NoError(t, err)

	targetExtractDir := filepath.Join(tempUploadDir, "tgz_dest")

	resp, err := sendFileAsStream(t, client, targetExtractDir, archiveName, tgzFileContent)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(targetExtractDir), filepath.Clean(*resp.FilePath))

	for name, expectedContent := range filesInArchive {
		extractedFilePath := filepath.Join(targetExtractDir, name)
		content, errRead := os.ReadFile(extractedFilePath)
		require.NoError(t, errRead, "Failed to read extracted file from tgz: %s", extractedFilePath)
		assert.Equal(t, expectedContent, string(content))
	}
}

func TestUploadFile_GzSingleFile(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempSourceDir := t.TempDir()
	tempUploadDir := t.TempDir()

	originalFilename := "logdata.txt"
	gzFilename := originalFilename + ".gz"
	expectedSavedFilename := originalFilename
	fileContent := "This is some gzipped data that is not a tar archive."

	gzFilePath := createTestGzFile(t, tempSourceDir, gzFilename, originalFilename, fileContent)
	gzFileContent, err := os.ReadFile(gzFilePath)
	require.NoError(t, err)

	targetDir := filepath.Join(tempUploadDir, "single_gz_upload")

	resp, err := sendFileAsStream(t, client, targetDir, gzFilename, gzFileContent)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")

	expectedSavedPath := filepath.Join(targetDir, expectedSavedFilename)
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedSavedPath), filepath.Clean(*resp.FilePath))

	decompressedContent, err := os.ReadFile(expectedSavedPath)
	require.NoError(t, err)
	assert.Equal(t, fileContent, string(decompressedContent))
}

func TestUploadFile_WithPathPrefix_Allowed(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempBaseDir := t.TempDir()

	originalPathPrefix := os.Getenv("PATH_PREFIX")
	defer func() {
		if setEnvErr := os.Setenv("PATH_PREFIX", originalPathPrefix); setEnvErr != nil {
			t.Logf("Failed to restore PATH_PREFIX: %v", setEnvErr)
		}
	}()

	pathPrefixForEnv := filepath.Join(tempBaseDir, "allowed_zone")
	errMk := os.MkdirAll(pathPrefixForEnv, 0755)
	require.NoError(t, errMk)
	if setEnvErr := os.Setenv("PATH_PREFIX", pathPrefixForEnv); setEnvErr != nil {
		t.Fatalf("Failed to set PATH_PREFIX: %v", setEnvErr)
	}
	t.Logf("PATH_PREFIX set to: %s", pathPrefixForEnv)

	fileName := "prefixed_file.txt"
	fileContent := "content within allowed prefix"
	targetDirUserProvided := filepath.Join(pathPrefixForEnv, "user_subdir")

	resp, err := sendFileAsStream(t, client, targetDirUserProvided, fileName, []byte(fileContent))
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	expectedFilePath := filepath.Join(targetDirUserProvided, fileName)
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedFilePath), filepath.Clean(*resp.FilePath))

	content, err := os.ReadFile(expectedFilePath)
	require.NoError(t, err)
	assert.Equal(t, fileContent, string(content))
}

func TestUploadFile_WithPathPrefix_Denied(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	originalPathPrefix := os.Getenv("PATH_PREFIX")
	defer func() {
		if setEnvErr := os.Setenv("PATH_PREFIX", originalPathPrefix); setEnvErr != nil {
			t.Logf("Failed to restore PATH_PREFIX: %v", setEnvErr)
		}
	}()

	tempBaseDir := t.TempDir()

	allowedPrefixDir := filepath.Join(tempBaseDir, "my_secure_area")
	errMk := os.MkdirAll(allowedPrefixDir, 0755)
	require.NoError(t, errMk)
	if setEnvErr := os.Setenv("PATH_PREFIX", allowedPrefixDir); setEnvErr != nil {
		t.Fatalf("Failed to set PATH_PREFIX: %v", setEnvErr)
	}
	t.Logf("PATH_PREFIX set to: %s", allowedPrefixDir)

	targetDirUserProvided := filepath.Join(tempBaseDir, "another_place")
	require.False(t, strings.Contains(filepath.ToSlash(targetDirUserProvided), filepath.ToSlash(allowedPrefixDir)), "Test setup error: targetDirUserProvided should not contain allowedPrefixDir for this denial test based on current isValidGrpcUploadPath logic.")

	fileName := "denied_access.txt"
	fileContent := "this content should be blocked"

	_, err := sendFileAsStream(t, client, targetDirUserProvided, fileName, []byte(fileContent))
	require.Error(t, err, "Expected permission denied error")

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())

	nonExistentFilePath := filepath.Join(targetDirUserProvided, fileName)
	_, statErr := os.Stat(nonExistentFilePath)
	assert.True(t, os.IsNotExist(statErr), "File should not have been created in the denied path.")
}

func TestUploadFile_PathPrefixNotSet(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	originalPathPrefix := os.Getenv("PATH_PREFIX")
	if setEnvErr := os.Setenv("PATH_PREFIX", ""); setEnvErr != nil {
		t.Fatalf("Failed to set PATH_PREFIX: %v", setEnvErr)
	}
	defer func() {
		if setEnvErr := os.Setenv("PATH_PREFIX", originalPathPrefix); setEnvErr != nil {
			t.Logf("Failed to restore PATH_PREFIX: %v", setEnvErr)
		}
	}()
	t.Logf("PATH_PREFIX explicitly cleared for test.")

	tempUploadDir := t.TempDir()
	fileName := "free_file.txt"
	fileContent := "no prefix, so anywhere (relative to server CWD or abs path) is fine"
	targetDir := filepath.Join(tempUploadDir, "open_zone")

	resp, err := sendFileAsStream(t, client, targetDir, fileName, []byte(fileContent))
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	expectedFilePath := filepath.Join(targetDir, fileName)
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedFilePath), filepath.Clean(*resp.FilePath))

	content, err := os.ReadFile(expectedFilePath)
	require.NoError(t, err)
	assert.Equal(t, fileContent, string(content))
}

func TestUploadFile_PathTraversal_Filename(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempBaseDir := t.TempDir()
	safeUploadSubDir := "safe_upload"

	if setEnvErr := os.Setenv("PATH_PREFIX", ""); setEnvErr != nil {
		t.Fatalf("Failed to set PATH_PREFIX: %v", setEnvErr)
	}
	defer func() {
		if unsetErr := os.Unsetenv("PATH_PREFIX"); unsetErr != nil {
			t.Logf("Failed to unset PATH_PREFIX: %v", unsetErr)
		}
	}()

	targetDirForUpload := filepath.Join(tempBaseDir, safeUploadSubDir)

	fileNameAttemptingTraversal := "../traversal_attempt.txt"
	fileContent := "malicious content"

	_, err := sendFileAsStream(t, client, targetDirForUpload, fileNameAttemptingTraversal, []byte(fileContent))
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Contains(t, st.Message(), "invalid characters or traversal attempt in filename")

	expectedTraversedPath := filepath.Join(tempBaseDir, "traversal_attempt.txt")
	_, statErr := os.Stat(expectedTraversedPath)
	assert.True(t, os.IsNotExist(statErr), "File should not exist at traversed path: %s", expectedTraversedPath)

	insideSafeDirPathCleaned := filepath.Clean(filepath.Join(targetDirForUpload, fileNameAttemptingTraversal))
	_, statErr = os.Stat(insideSafeDirPathCleaned)
	assert.True(t, os.IsNotExist(statErr), "File should not exist inside safe dir with traversal name: %s", insideSafeDirPathCleaned)
}

func TestUploadFile_PathTraversal_TarArchive(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempSourceDir := t.TempDir()
	tempUploadBase := t.TempDir()

	if setEnvErr := os.Setenv("PATH_PREFIX", ""); setEnvErr != nil {
		t.Fatalf("Failed to set PATH_PREFIX: %v", setEnvErr)
	}
	defer func() {
		if unsetErr := os.Unsetenv("PATH_PREFIX"); unsetErr != nil {
			t.Logf("Failed to unset PATH_PREFIX: %v", unsetErr)
		}
	}()

	archiveName := "malicious.tar"
	filesInArchive := map[string]string{
		"good_file_in_tar.txt": "innocent content",
		"../evil_from_tar.txt": "evil content that should not be extracted",
	}
	tarFilePath := createTestTarArchive(t, tempSourceDir, archiveName, filesInArchive)
	tarFileContent, err := os.ReadFile(tarFilePath)
	require.NoError(t, err)

	targetExtractDir := filepath.Join(tempUploadBase, "tar_traversal_dest")

	_, err = sendFileAsStream(t, client, targetExtractDir, archiveName, tarFileContent)
	require.Error(t, err, "Expected an error due to path traversal in tar archive")

	_, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status error")

	expectedEvilPath := filepath.Join(tempUploadBase, "evil_from_tar.txt")
	_, statErr := os.Stat(expectedEvilPath)
	assert.True(t, os.IsNotExist(statErr), "Traversed file from tar should not exist: %s", expectedEvilPath)

}

func TestUploadFile_EmptyStream_NonArchive(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	targetDir := t.TempDir()
	fileName := "empty_file.txt"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.UploadFile(ctx)
	require.NoError(t, err)

	fileInfo := &pb.FileInfo{Path: &targetDir, Filename: &fileName}
	req := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_Info{Info: fileInfo}}
	err = stream.Send(req)
	require.NoError(t, err)

	resp, err := stream.CloseAndRecv()
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")

	expectedPath := filepath.Join(targetDir, fileName)
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedPath), filepath.Clean(*resp.FilePath))

	stat, err := os.Stat(expectedPath)
	require.NoError(t, err)
	assert.Equal(t, int64(0), stat.Size(), "Created file should be empty")
}

func TestUploadFile_EmptyStream_TarArchive(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	targetDir := t.TempDir()
	fileName := "empty.tar"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.UploadFile(ctx)
	require.NoError(t, err)

	fileInfo := &pb.FileInfo{Path: &targetDir, Filename: &fileName}
	req := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_Info{Info: fileInfo}}
	err = stream.Send(req)
	require.NoError(t, err)

	_, err = stream.CloseAndRecv()
	require.Error(t, err, "Expected error for empty tar stream")
	st, ok := status.FromError(err)
	require.True(t, ok)
	expectedMsg := "empty or invalid tar archive 'empty.tar': no headers found"
	assert.Contains(t, st.Message(), expectedMsg, "Error message mismatch")
}

func TestUploadFile_IncompleteTarArchive(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempUploadDir := t.TempDir()
	archiveName := "incomplete.tar"
	targetExtractDir := filepath.Join(tempUploadDir, "incomplete_tar_dest")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: "only_header.txt",
		Mode: 0600,
	}
	err := tw.WriteHeader(hdr)
	require.NoError(t, err)

	var bufCorrupt bytes.Buffer
	twCorrupt := tar.NewWriter(&bufCorrupt)
	content1 := "This is file one."
	hdr1 := &tar.Header{Name: "file1.txt", Mode: 0600, Size: int64(len(content1)), ModTime: time.Now()}
	require.NoError(t, twCorrupt.WriteHeader(hdr1))
	_, err = twCorrupt.Write([]byte(content1))
	require.NoError(t, err)
	hdr2 := &tar.Header{Name: "file2_incomplete.txt", Mode: 0600, Size: 500, ModTime: time.Now()}
	require.NoError(t, twCorrupt.WriteHeader(hdr2))

	_, err = sendFileAsStream(t, client, targetExtractDir, archiveName, bufCorrupt.Bytes())
	require.Error(t, err, "Expected error for incomplete/corrupt tar archive")

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.True(t, strings.Contains(st.Message(), "failed to copy content") || strings.Contains(st.Message(), "unexpected EOF"),
		"Error message should indicate tar processing issue. Got: %s", st.Message())

	_, statErr := os.Stat(filepath.Join(targetExtractDir, "file2_incomplete.txt"))
	assert.True(t, os.IsNotExist(statErr), "Incomplete file from tar should not exist.")
}

func TestUploadFile_PutLikeBehavior_Directory(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempBaseDir := t.TempDir()
	targetDir := filepath.Join(tempBaseDir, "upload_dir")
	fileName := "file_for_put.txt"
	fileContent := "content for put-like upload"

	resp, err := sendFileAsStream(t, client, targetDir, fileName, []byte(fileContent))
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	expectedFilePath := filepath.Join(targetDir, fileName)
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedFilePath), filepath.Clean(*resp.FilePath))

	dirStat, err := os.Stat(targetDir)
	require.NoError(t, err, "Target directory should have been created.")
	assert.True(t, dirStat.IsDir(), "Target path should be a directory.")

	uploadedContent, err := os.ReadFile(expectedFilePath)
	require.NoError(t, err)
	assert.Equal(t, fileContent, string(uploadedContent))

	fileName2 := "file_for_put2.txt"
	fileContent2 := "content for put-like upload 2"
	resp2, err2 := sendFileAsStream(t, client, targetDir, fileName2, []byte(fileContent2))
	require.NoError(t, err2)
	require.NotNil(t, resp2)
	assert.Contains(t, *resp2.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	expectedFilePath2 := filepath.Join(targetDir, fileName2)
	require.NotNil(t, resp2.FilePath)
	assert.Equal(t, filepath.Clean(expectedFilePath2), filepath.Clean(*resp2.FilePath))

	uploadedContent2, err := os.ReadFile(expectedFilePath2)
	require.NoError(t, err)
	assert.Equal(t, fileContent2, string(uploadedContent2))
}

func TestUploadFile_NonExistentDeepPath_Creation(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempBaseDir := t.TempDir()
	deepPath := filepath.Join(tempBaseDir, "level1", "level2", "level3")
	fileName := "deep_file.txt"
	fileContent := "content in a deep path"

	resp, err := sendFileAsStream(t, client, deepPath, fileName, []byte(fileContent))
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	expectedFilePath := filepath.Join(deepPath, fileName)
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedFilePath), filepath.Clean(*resp.FilePath))

	dirStat, err := os.Stat(deepPath)
	require.NoError(t, err, "Deep target directory should have been created.")
	assert.True(t, dirStat.IsDir(), "Deep target path should be a directory.")

	uploadedContent, err := os.ReadFile(expectedFilePath)
	require.NoError(t, err)
	assert.Equal(t, fileContent, string(uploadedContent))
}

func TestUploadFile_MissingFileInfo_FirstMessage(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.UploadFile(ctx)
	require.NoError(t, err)

	chunkReq := &pb.UploadFileRequest{
		Data: &pb.UploadFileRequest_ChunkData{ChunkData: []byte("some data")},
	}
	err = stream.Send(chunkReq)
	require.NoError(t, err)
	_, err = stream.CloseAndRecv()

	require.Error(t, err, "Expected error when FileInfo is not the first message")
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUploadFile_UnexpectedFileInfo_AfterFirstMessage(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	targetDir := t.TempDir()
	fileName := "test.txt"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.UploadFile(ctx)
	require.NoError(t, err)

	validFileInfo := &pb.FileInfo{Path: &targetDir, Filename: &fileName}
	infoReq := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_Info{Info: validFileInfo}}
	err = stream.Send(infoReq)
	require.NoError(t, err)

	chunkReq := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_ChunkData{ChunkData: []byte("data...")}}
	err = stream.Send(chunkReq)
	require.NoError(t, err)

	anotherFileName := "another.txt"
	unexpectedFileInfo := &pb.FileInfo{Path: &targetDir, Filename: &anotherFileName}
	infoReqUnexpected := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_Info{Info: unexpectedFileInfo}}
	err = stream.Send(infoReqUnexpected)
	if err == nil {
		_, err = stream.CloseAndRecv()
	}

	require.Error(t, err, "Expected error when FileInfo is sent after the first message")
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUploadFile_EmptyPath_InFileInfo(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	emptyPath := ""
	fileName := "file_with_empty_path.txt"

	_, err := sendFileAsStream(t, client, emptyPath, fileName, []byte("content"))
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUploadFile_EmptyFilename_InFileInfo(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	targetDir := t.TempDir()
	emptyFilename := ""

	_, err := sendFileAsStream(t, client, targetDir, emptyFilename, []byte("content"))
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUploadFile_LoneGzNotTar(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempSourceDir := t.TempDir()
	tempUploadDir := t.TempDir()

	originalFilename := "single_file_log.txt"
	gzFilename := originalFilename + ".gz"
	expectedSavedFilename := originalFilename
	fileContent := "This is a single gzipped file, not a tarball."

	gzFilePath := createTestGzFile(t, tempSourceDir, gzFilename, originalFilename, fileContent)
	gzFileBytes, err := os.ReadFile(gzFilePath)
	require.NoError(t, err)

	targetDir := filepath.Join(tempUploadDir, "lone_gz_dest")

	resp, err := sendFileAsStream(t, client, targetDir, gzFilename, gzFileBytes)
	require.NoError(t, err, "Uploading a lone .gz file should succeed.")
	require.NotNil(t, resp)
	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")

	expectedSavedPath := filepath.Join(targetDir, expectedSavedFilename)

	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedSavedPath), filepath.Clean(*resp.FilePath))

	savedContent, err := os.ReadFile(expectedSavedPath)
	require.NoError(t, err, "Failed to read the uploaded and decompressed file.")
	assert.Equal(t, fileContent, string(savedContent), "Decompressed content should match original.")
}

func TestUploadFile_GzFileInvalidTarContent(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempSourceDir := t.TempDir()
	tempUploadDir := t.TempDir()

	archiveName := "fake_archive.tar.gz"
	originalTextFilename := "not_tar.txt"
	textContent := "This is just plain text, gzipped, but named like a tar.gz"
	expectedMsgPart := "failed to read tar header"

	gzFilePath := createTestGzFile(t, tempSourceDir, archiveName, originalTextFilename, textContent)
	gzFileBytes, err := os.ReadFile(gzFilePath)
	require.NoError(t, err)

	targetDir := filepath.Join(tempUploadDir, "fake_tgz_dest")

	_, err = sendFileAsStream(t, client, targetDir, archiveName, gzFileBytes)
	require.Error(t, err, "Expected error when .tar.gz contains non-tar gzipped data.")

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code(), "Expected InvalidArgument for tar processing failure")
	assert.Contains(t, st.Message(), expectedMsgPart, "Error message should indicate tar header reading failure.")
	assert.Contains(t, st.Message(), "unexpected EOF", "Underlying error for this specific case should be 'unexpected EOF'")

	items, _ := os.ReadDir(targetDir)
	assert.Len(t, items, 0, "Target directory should be empty after failed tar processing.")
}

func TestUploadFile_TgzFile_CorruptTarData(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempUploadDir := t.TempDir()
	archiveName := "corrupt_data.tgz"
	targetDir := filepath.Join(tempUploadDir, "corrupt_tgz_dest")
	expectedMsgPart := "failed to read tar header"

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	_, err := gzw.Write([]byte("corrupted data"))
	require.NoError(t, err)
	require.NoError(t, gzw.Close())

	corruptTgzBytes := buf.Bytes()

	_, err = sendFileAsStream(t, client, targetDir, archiveName, corruptTgzBytes)
	require.Error(t, err, "Expected error for tgz with corrupt tar data.")

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Contains(t, st.Message(), expectedMsgPart, "Error message should indicate tar header reading failure.")

	items, _ := os.ReadDir(targetDir)
	assert.Len(t, items, 0, "Target directory should be empty after failed corrupt tgz processing.")
}
