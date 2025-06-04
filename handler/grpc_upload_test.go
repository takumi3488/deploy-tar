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

	pb "deploytar/proto/deploytar/proto/fileservice/v1" // Assuming this is the correct proto path based on go_package and grpc_list.go

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// setupTestGRPCServer sets up an in-process gRPC server for testing.
// It returns a FileServiceClient and a cleanup function.
func setupTestGRPCServer(t *testing.T) (pb.FileServiceClient, func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	// GRPCListDirectoryServer (from grpc_list.go) implements FileServiceServer.
	// The UploadFile method is part of this server type.
	serverInstance := NewGRPCListDirectoryServer()
	s := grpc.NewServer()
	pb.RegisterFileServiceServer(s, serverInstance)

	go func() {
		if errS := s.Serve(lis); errS != nil && !strings.Contains(errS.Error(), "use of closed network connection") {
			t.Logf("gRPC server Serve error: %v", errS) // Log unexpected server errors.
		}
	}()

	_, cancelConn := context.WithTimeout(context.Background(), 10*time.Second) // Increased timeout for CI
	defer cancelConn()

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err, "Failed to connect to gRPC server at %s. Ensure server goroutine started.", lis.Addr().String())

	// Wait for connection to be ready
	connCtx2, cancelConn2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelConn2()

	// Simple approach: try to make a test call to verify connection
	client := pb.NewFileServiceClient(conn)

	// Wait until we can successfully make a call or timeout
	for {
		rootDir := "/"
		_, err := client.ListDirectory(connCtx2, &pb.ListDirectoryRequest{Directory: &rootDir})
		if err == nil {
			break // Connection is ready
		}
		if connCtx2.Err() != nil {
			t.Fatal("Failed to establish connection within timeout")
		}
		time.Sleep(100 * time.Millisecond)
	}

	cleanup := func() {
		s.GracefulStop()    // Gracefully stop the server.
		err := conn.Close() // Close the client connection.
		if err != nil {
			t.Logf("Error closing client connection: %v", err)
		}
		err = lis.Close() // Close the listener.
		if err != nil {
			t.Logf("Error closing listener: %v", err)
		}
	}

	return client, cleanup
}

// createTestTextFile creates a simple text file for testing.
// createTestTarArchive creates a tar archive for testing.
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
			Name:    name,
			Mode:    0600,
			Size:    int64(len(content)),
			ModTime: time.Now(), // Ensure consistent metadata if needed
		}
		err = tw.WriteHeader(hdr)
		require.NoError(t, err)
		_, err = tw.Write([]byte(content))
		require.NoError(t, err)
	}
	return archivePath
}

// createTestTgzArchive creates a tgz (tar.gz) archive for testing.
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

// createTestGzFile creates a gz (gzip compressed) single file for testing.
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
	gzw.Name = originalFilename // Set original filename, handler might use it
	defer func() {
		if closeErr := gzw.Close(); closeErr != nil {
			t.Logf("Failed to close gzip writer: %v", closeErr)
		}
	}()

	_, err = gzw.Write([]byte(content))
	require.NoError(t, err)
	return gzPath
}

// sendFileAsStream is a helper to send a file via the gRPC UploadFile stream.
func sendFileAsStream(t *testing.T, client pb.FileServiceClient, targetPath, sourceFilename string, fileContent []byte) (*pb.UploadFileResponse, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second) // Generous timeout for stream operations
	defer cancel()

	stream, err := client.UploadFile(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to create upload stream: %v", err)
	}

	// 1. Send FileInfo
	fileInfo := &pb.FileInfo{
		Path:     &targetPath,
		Filename: &sourceFilename,
	}
	req := &pb.UploadFileRequest{
		Data: &pb.UploadFileRequest_Info{Info: fileInfo},
	}
	if err = stream.Send(req); err != nil {
		_, recvErr := stream.CloseAndRecv() // Try to get server error
		if recvErr != nil {
			return nil, status.Errorf(codes.Internal, "Failed to send file info (send err: %v). Server error on CloseAndRecv: %v", err, recvErr)
		}
		return nil, status.Errorf(codes.Internal, "Failed to send file info: %v. Server did not return specific error on CloseAndRecv.", err)
	}

	// 2. Send file content in chunks
	buffer := make([]byte, 1024) // 1KB chunk size
	reader := bytes.NewReader(fileContent)

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

	// 3. Close stream and receive response
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
	tempUploadDir := t.TempDir() // Server's base path for extraction

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
	gzFilename := originalFilename + ".gz" // e.g. logdata.txt.gz
	fileContent := "This is some gzipped data that is not a tar archive."

	gzFilePath := createTestGzFile(t, tempSourceDir, gzFilename, originalFilename, fileContent)
	gzFileContent, err := os.ReadFile(gzFilePath)
	require.NoError(t, err)

	targetDir := filepath.Join(tempUploadDir, "single_gz_upload")

	resp, err := sendFileAsStream(t, client, targetDir, gzFilename, gzFileContent)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")

	expectedSavedFilename := strings.TrimSuffix(gzFilename, ".gz") // Handler should remove .gz
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

	originalPathPrefix := os.Getenv("PATH_PREFIX")
	defer func() {
		if setEnvErr := os.Setenv("PATH_PREFIX", originalPathPrefix); setEnvErr != nil {
			t.Logf("Failed to restore PATH_PREFIX: %v", setEnvErr)
		}
	}()

	tempBaseDir := t.TempDir() // Base for creating prefix and target dirs

	// Define an absolute path for PATH_PREFIX for clarity
	// The handler's path validation logic (isValidGrpcUploadPath) compares components.
	// If PATH_PREFIX = /abs/path/prefix
	// And user uploads to /abs/path/prefix/data, it should be allowed.
	pathPrefixForEnv := filepath.Join(tempBaseDir, "allowed_zone")
	errMk := os.MkdirAll(pathPrefixForEnv, 0755) // Ensure prefix dir exists for some test setups, though handler might not require it
	require.NoError(t, errMk)
	if setEnvErr := os.Setenv("PATH_PREFIX", pathPrefixForEnv); setEnvErr != nil {
		t.Fatalf("Failed to set PATH_PREFIX: %v", setEnvErr)
	}
	t.Logf("PATH_PREFIX set to: %s", pathPrefixForEnv)

	fileName := "prefixed_file.txt"
	fileContent := "content within allowed prefix"
	// User provides a path that should be valid under the prefix
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

	// Attempt to upload to a path not matching the prefix logic
	// e.g. PATH_PREFIX = /tmp/Test.../my_secure_area
	//      targetDirUserProvided = /tmp/Test.../another_place (not containing "my_secure_area" as a path component sequence)
	targetDirUserProvided := filepath.Join(tempBaseDir, "another_place")
	// Ensure this path does not accidentally satisfy the prefix condition for a robust test
	require.False(t, strings.Contains(filepath.ToSlash(targetDirUserProvided), filepath.ToSlash(allowedPrefixDir)), "Test setup error: targetDirUserProvided should not contain allowedPrefixDir for this denial test based on current isValidGrpcUploadPath logic.")

	fileName := "denied_access.txt"
	fileContent := "this content should be blocked"

	_, err := sendFileAsStream(t, client, targetDirUserProvided, fileName, []byte(fileContent))
	require.Error(t, err, "Expected permission denied error")

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
	assert.Contains(t, st.Message(), "is outside the scope of path prefix") // Updated to service error

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
	if setEnvErr := os.Setenv("PATH_PREFIX", ""); setEnvErr != nil {
		t.Fatalf("Failed to set PATH_PREFIX: %v", setEnvErr)
	}
	defer func() {
		if unsetErr := os.Unsetenv("PATH_PREFIX"); unsetErr != nil {
			t.Logf("Failed to unset PATH_PREFIX: %v", unsetErr)
		}
	}()

	tempBaseDir := t.TempDir()         // Represents a directory the server might be running in or have access to
	safeUploadSubDir := "safe_uploads" // The intended subdirectory for uploads in this test
	targetDirForUpload := filepath.Join(tempBaseDir, safeUploadSubDir)
	// Handler will do MkdirAll on targetDirForUpload

	fileNameAttemptingTraversal := "../traversal_attempt.txt"
	fileContent := "malicious content"

	_, err := sendFileAsStream(t, client, targetDirForUpload, fileNameAttemptingTraversal, []byte(fileContent))
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code()) // Service correctly makes it PermissionDenied
	// Message from service: "invalid characters or traversal attempt in filename '%s'"
	assert.Contains(t, st.Message(), "invalid characters or traversal attempt in filename")

	// Verify file was NOT created at the intended traversed path
	// targetDirForUpload = /tmp/.../safe_uploads
	// fileNameAttemptingTraversal = ../traversal_attempt.txt
	// Cleaned path would be /tmp/.../traversal_attempt.txt
	expectedTraversedPath := filepath.Join(tempBaseDir, "traversal_attempt.txt")
	_, statErr := os.Stat(expectedTraversedPath)
	assert.True(t, os.IsNotExist(statErr), "File should not exist at traversed path: %s", expectedTraversedPath)

	// Also ensure it wasn't created inside the safe dir with a weird name
	insideSafeDirPathCleaned := filepath.Clean(filepath.Join(targetDirForUpload, fileNameAttemptingTraversal))
	_, statErr = os.Stat(insideSafeDirPathCleaned)
	assert.True(t, os.IsNotExist(statErr), "File should not exist inside safe dir with traversal name: %s", insideSafeDirPathCleaned)
}

func TestUploadFile_PathTraversal_TarArchive(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()
	if setEnvErr := os.Setenv("PATH_PREFIX", ""); setEnvErr != nil {
		t.Fatalf("Failed to set PATH_PREFIX: %v", setEnvErr)
	}
	defer func() {
		if unsetErr := os.Unsetenv("PATH_PREFIX"); unsetErr != nil {
			t.Logf("Failed to unset PATH_PREFIX: %v", unsetErr)
		}
	}()

	tempSourceDir := t.TempDir()  // To create the malicious tar
	tempUploadBase := t.TempDir() // Base for server's extraction attempt

	archiveName := "malicious.tar"
	filesInArchive := map[string]string{
		"good_file_in_tar.txt": "innocent content",
		"../evil_from_tar.txt": "sneaky tar content", // Path traversal
	}
	tarFilePath := createTestTarArchive(t, tempSourceDir, archiveName, filesInArchive)
	tarFileContent, err := os.ReadFile(tarFilePath)
	require.NoError(t, err)

	targetExtractDir := filepath.Join(tempUploadBase, "tar_traversal_dest")

	_, err = sendFileAsStream(t, client, targetExtractDir, archiveName, tarFileContent)
	// This test expects the handler's tar extraction logic to prevent traversal.
	// If the handler is vulnerable, err might be nil, and the file would be written outside.
	// If the handler is secure, it should return an error.
	require.Error(t, err, "Expected an error due to path traversal in tar archive")

	_, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status error")
	// The exact error code might depend on how the handler detects/reports this.
	// PermissionDenied or InvalidArgument are common.
	// Let's assume it's a general error indicating failure.
	// A more specific check could be:
	// assert.Equal(t, codes.InvalidArgument, st.Code(), "Expected InvalidArgument for traversal attempt in tar")
	// assert.Contains(t, st.Message(), "path traversal attempt", "Error message should indicate traversal attempt")
	// For now, just checking for any error is a start.
	// The handler's UploadFile should ideally return a specific error for this.

	// Verify the traversed file was NOT created
	expectedEvilPath := filepath.Join(tempUploadBase, "evil_from_tar.txt")
	_, statErr := os.Stat(expectedEvilPath)
	assert.True(t, os.IsNotExist(statErr), "Traversed file from tar should not exist: %s", expectedEvilPath)

	// Verify the good file MAY or MAY NOT exist, depending on handler's atomicity.
	// If the handler stops on first error, good_file_in_tar.txt might not be created.
	// If it processes all entries and then errors, it might.
	// For this test, we primarily care that the evil file isn't created.
	// goodFilePath := filepath.Join(targetExtractDir, "good_file_in_tar.txt")
	// _, goodFileStatErr := os.Stat(goodFilePath)
	// t.Logf("Stat for good file (%s): %v", goodFilePath, goodFileStatErr)
}

func TestUploadFile_EmptyStream_NonArchive(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	targetDir := t.TempDir()
	fileName := "empty_file.txt"

	// Send only FileInfo, then immediately CloseAndRecv
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.UploadFile(ctx)
	require.NoError(t, err)

	fileInfo := &pb.FileInfo{Path: &targetDir, Filename: &fileName}
	req := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_Info{Info: fileInfo}}
	err = stream.Send(req)
	require.NoError(t, err)

	resp, err := stream.CloseAndRecv() // No chunks sent
	require.NoError(t, err)            // Expect success for zero-byte file
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
	fileName := "empty.tar" // Sending a .tar extension but no content

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.UploadFile(ctx)
	require.NoError(t, err)

	fileInfo := &pb.FileInfo{Path: &targetDir, Filename: &fileName}
	req := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_Info{Info: fileInfo}}
	err = stream.Send(req)
	require.NoError(t, err)

	_, err = stream.CloseAndRecv() // No chunks sent
	// Behavior for empty tar might be an error or successful "empty extraction"
	// Current handler logic for tar expects non-empty content.
	require.Error(t, err, "Expected error for empty tar stream")
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code()) // Service returns InvalidArgument
	// Service message: "empty or invalid tar archive '%s': no headers found"
	expectedMsg := "empty or invalid tar archive 'empty.tar': no headers found"
	assert.Contains(t, st.Message(), expectedMsg, "Error message mismatch")
}

func TestUploadFile_IncompleteTarArchive(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempUploadDir := t.TempDir()
	archiveName := "incomplete.tar"
	targetExtractDir := filepath.Join(tempUploadDir, "incomplete_tar_dest")

	// Create a tar file header but no actual content for that file, or truncated tar
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: "only_header.txt",
		Mode: 0600,
		Size: 100, // Declares size but no data will follow for this file in tar
	}
	err := tw.WriteHeader(hdr)
	require.NoError(t, err)
	// tw.Close() // Deliberately not writing the full tar or closing properly for some scenarios
	// For this test, let's send just the header part of the tar stream.
	// Or, send a tar that's been cut off mid-stream.

	// Scenario 1: Send only a header, then close stream
	// This simulates a tar file that only contains a header for a file but no data for it.
	// The tar.Reader might encounter io.EOF or io.ErrUnexpectedEOF when trying to read the file's content.

	// For a more robust test of "incomplete", let's create a tar with one full file,
	// then a header for a second, then truncate.
	var bufCorrupt bytes.Buffer
	twCorrupt := tar.NewWriter(&bufCorrupt)
	// File 1 (complete)
	content1 := "This is file one."
	hdr1 := &tar.Header{Name: "file1.txt", Mode: 0600, Size: int64(len(content1)), ModTime: time.Now()}
	require.NoError(t, twCorrupt.WriteHeader(hdr1))
	_, err = twCorrupt.Write([]byte(content1))
	require.NoError(t, err)
	// File 2 (header only, data will be missing from stream)
	hdr2 := &tar.Header{Name: "file2_incomplete.txt", Mode: 0600, Size: 500, ModTime: time.Now()}
	require.NoError(t, twCorrupt.WriteHeader(hdr2))
	// twCorrupt.Close() // Don't close, so it's not a "valid" end of archive.
	// We will send bufCorrupt.Bytes() which is an incomplete tar stream.

	_, err = sendFileAsStream(t, client, targetExtractDir, archiveName, bufCorrupt.Bytes())
	require.Error(t, err, "Expected error for incomplete/corrupt tar archive")

	st, ok := status.FromError(err)
	require.True(t, ok)
	// The specific error message/code depends on how the tar library and service report it.
	// service.UploadFile -> extractTar -> "failed to read tar header from archive '%s': %w"
	// The underlying error for incomplete tar is often io.ErrUnexpectedEOF or just io.EOF from tar.Reader.Next()
	assert.Equal(t, codes.InvalidArgument, st.Code()) // Service returns InvalidArgument for bad archives
	assert.True(t, strings.Contains(st.Message(), "failed to read tar header") ||
		strings.Contains(st.Message(), "unexpected EOF"), // Underlying tar lib error
		"Error message should indicate tar processing issue. Got: %s", st.Message())

	// Check that file1.txt might or might not exist, but file2_incomplete.txt should not.
	_, statErr := os.Stat(filepath.Join(targetExtractDir, "file2_incomplete.txt"))
	assert.True(t, os.IsNotExist(statErr), "Incomplete file from tar should not exist.")
}

func TestUploadFile_PutLikeBehavior_Directory(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempBaseDir := t.TempDir()
	targetDir := filepath.Join(tempBaseDir, "put_target_dir") // This dir does not exist initially
	fileName := "file_for_put.txt"
	fileContent := "content for put-like upload"

	// The handler should create `targetDir` if it doesn't exist.
	resp, err := sendFileAsStream(t, client, targetDir, fileName, []byte(fileContent))
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	expectedFilePath := filepath.Join(targetDir, fileName)
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedFilePath), filepath.Clean(*resp.FilePath))

	// Verify targetDir was created
	dirStat, err := os.Stat(targetDir)
	require.NoError(t, err, "Target directory should have been created.")
	assert.True(t, dirStat.IsDir(), "Target path should be a directory.")

	// Verify file content
	uploadedContent, err := os.ReadFile(expectedFilePath)
	require.NoError(t, err)
	assert.Equal(t, fileContent, string(uploadedContent))

	// Test again, but targetDir already exists
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
	deepPath := filepath.Join(tempBaseDir, "a", "b", "c", "d") // None of these exist
	fileName := "deep_file.txt"
	fileContent := "content in a deep path"

	resp, err := sendFileAsStream(t, client, deepPath, fileName, []byte(fileContent))
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")
	expectedFilePath := filepath.Join(deepPath, fileName)
	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedFilePath), filepath.Clean(*resp.FilePath))

	// Verify deepPath was created
	dirStat, err := os.Stat(deepPath)
	require.NoError(t, err, "Deep target directory should have been created.")
	assert.True(t, dirStat.IsDir(), "Deep target path should be a directory.")

	// Verify file content
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

	// Send chunk data as the first message, without FileInfo
	chunkReq := &pb.UploadFileRequest{
		Data: &pb.UploadFileRequest_ChunkData{ChunkData: []byte("some data")},
	}
	err = stream.Send(chunkReq)
	// The client-side stream.Send might not error immediately.
	// The error should come from CloseAndRecv or a subsequent Send.
	if err == nil { // If Send itself didn't error (it might not for client stream)
		_, err = stream.CloseAndRecv()
	}

	require.Error(t, err, "Expected error when FileInfo is not the first message")
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "Missing FileInfo in the first message") // Updated to match handler's direct error
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

	// 1. Send valid FileInfo
	validFileInfo := &pb.FileInfo{Path: &targetDir, Filename: &fileName}
	infoReq := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_Info{Info: validFileInfo}}
	err = stream.Send(infoReq)
	require.NoError(t, err)

	// 2. Send some chunk data (optional, but makes test more realistic)
	chunkReq := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_ChunkData{ChunkData: []byte("data...")}}
	err = stream.Send(chunkReq)
	require.NoError(t, err)

	// 3. Send another FileInfo (unexpected)
	anotherTargetDir := t.TempDir() // Ensure it's a different pointer if that matters
	anotherFileName := "another.txt"
	unexpectedFileInfo := &pb.FileInfo{Path: &anotherTargetDir, Filename: &anotherFileName} // Content doesn't matter as much as type
	infoReqUnexpected := &pb.UploadFileRequest{Data: &pb.UploadFileRequest_Info{Info: unexpectedFileInfo}}
	err = stream.Send(infoReqUnexpected)

	if err == nil { // If Send itself didn't error
		_, err = stream.CloseAndRecv()
	}

	require.Error(t, err, "Expected error when FileInfo is sent after the first message")
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "Received FileInfo message after the first one") // Updated to match handler's direct error
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
	assert.Contains(t, st.Message(), "Target path is required in FileInfo") // Updated to match handler's direct error
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
	assert.Contains(t, st.Message(), "Filename is required in FileInfo") // Updated to match handler's direct error
}

// TestUploadFile_LoneGzNotTar tests uploading a .gz file that is NOT a tar archive.
// It should be saved as-is (after decompression if handler does that for .gz).
func TestUploadFile_LoneGzNotTar(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempSourceDir := t.TempDir()
	tempUploadDir := t.TempDir()

	originalFilename := "single_file_log.txt"
	gzFilename := originalFilename + ".gz"
	fileContent := "This is a single gzipped file, not a tarball."

	// Create a .gz file
	gzFilePath := createTestGzFile(t, tempSourceDir, gzFilename, originalFilename, fileContent)
	gzFileBytes, err := os.ReadFile(gzFilePath)
	require.NoError(t, err)

	targetDir := filepath.Join(tempUploadDir, "lone_gz_dest")

	resp, err := sendFileAsStream(t, client, targetDir, gzFilename, gzFileBytes)
	require.NoError(t, err, "Uploading a lone .gz file should succeed.")
	require.NotNil(t, resp)
	assert.Contains(t, *resp.Message, "processed successfully", "Expected success message to contain 'processed successfully'")

	// The handler is expected to decompress .gz files and save with original name
	// (or name derived from gzFilename if original not in header).
	// If createTestGzFile sets gzw.Name, handler might use it.
	// Otherwise, it might strip .gz from gzFilename.
	expectedSavedFilename := originalFilename // Assuming handler uses gzw.Name or smart stripping
	expectedSavedPath := filepath.Join(targetDir, expectedSavedFilename)

	require.NotNil(t, resp.FilePath)
	assert.Equal(t, filepath.Clean(expectedSavedPath), filepath.Clean(*resp.FilePath))

	savedContent, err := os.ReadFile(expectedSavedPath)
	require.NoError(t, err, "Failed to read the uploaded and decompressed file.")
	assert.Equal(t, fileContent, string(savedContent), "Decompressed content should match original.")
}

// TestUploadFile_GzFileInvalidTarContent tests uploading a .gz file that is named like a .tar.gz
// but its content is just gzipped text, not a tar archive.
func TestUploadFile_GzFileInvalidTarContent(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempSourceDir := t.TempDir()
	tempUploadDir := t.TempDir()

	// Name it like a tgz, but content is just gzipped text
	archiveName := "fake_archive.tar.gz"
	originalTextFilename := "not_a_tar.txt" // Name for gzip header
	textContent := "This is just plain text, gzipped, but named like a tar.gz"

	gzFilePath := createTestGzFile(t, tempSourceDir, archiveName, originalTextFilename, textContent)
	gzFileBytes, err := os.ReadFile(gzFilePath)
	require.NoError(t, err)

	targetDir := filepath.Join(tempUploadDir, "fake_tgz_dest")

	// The handler will see ".tar.gz", try to decompress then untar.
	// Decompression will succeed, but untarring plain text will fail.
	_, err = sendFileAsStream(t, client, targetDir, archiveName, gzFileBytes)
	require.Error(t, err, "Expected error when .tar.gz contains non-tar gzipped data.")

	st, ok := status.FromError(err)
	require.True(t, ok)
	// Service/handler maps this to InvalidArgument as it's a problem with client-provided data format
	assert.Equal(t, codes.InvalidArgument, st.Code(), "Expected InvalidArgument for tar processing failure")
	expectedMsgPart := "failed to read tar header from archive 'fake_archive.tar.gz'" // Service error
	assert.Contains(t, st.Message(), expectedMsgPart, "Error message should indicate tar header reading failure.")
	// Underlying error from tar library for this case is often "unexpected EOF"
	assert.Contains(t, st.Message(), "unexpected EOF", "Underlying error for this specific case should be 'unexpected EOF'")

	// Ensure no partial files were left in the target directory
	// (e.g., the decompressed non-tar file)
	items, _ := os.ReadDir(targetDir)
	assert.Len(t, items, 0, "Target directory should be empty after failed tar processing.")
}

func TestUploadFile_TgzFile_CorruptTarData(t *testing.T) {
	client, cleanup := setupTestGRPCServer(t)
	defer cleanup()

	tempUploadDir := t.TempDir()
	archiveName := "corrupt_data.tgz"
	targetDir := filepath.Join(tempUploadDir, "corrupt_tgz_dest")

	// Create a tgz file that contains non-tar data (empty gzipped content)
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	_, err := gzw.Write([]byte{}) // Intentionally empty data, not a tar header
	require.NoError(t, err)
	require.NoError(t, gzw.Close())

	corruptTgzBytes := buf.Bytes()

	_, err = sendFileAsStream(t, client, targetDir, archiveName, corruptTgzBytes)
	require.Error(t, err, "Expected error for tgz with corrupt tar data.")

	st, ok := status.FromError(err)
	require.True(t, ok)
	// Handler message: "empty or invalid tar archive '%s': no headers found"
	assert.Equal(t, codes.InvalidArgument, st.Code(), "Expected InvalidArgument for corrupt tar data") // Service error
	expectedMsgPart := "empty or invalid tar archive 'corrupt_data.tgz'" // Service error for empty/no-header tars
	assert.Contains(t, st.Message(), expectedMsgPart, "Error message should indicate tar header reading failure.")

	items, _ := os.ReadDir(targetDir)
	assert.Len(t, items, 0, "Target directory should be empty after failed corrupt tgz processing.")
}

// TODO: Add more tests:
// - Concurrent uploads (if supported/relevant)
// - Very large file uploads (chunking logic, timeouts)
// - Network interruption during stream
// - File permissions on server (if handler sets specific perms)
// - Uploading to a path that is a file, not a directory
// - Server disk full (hard to test reliably in unit tests)
