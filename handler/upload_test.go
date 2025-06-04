package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"encoding/json" // Added for JSON unmarshalling

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require" // Added for require.NoError etc.
)

// createTestArchive creates an in-memory tar or tar.gz archive for testing.
// It determines whether to use gzip compression based on the extension of archiveFilename.
func createTestArchive(t *testing.T, files map[string]string, dirs []string, archiveFilename string) *bytes.Buffer {
	buf := new(bytes.Buffer)
	var tw *tar.Writer
	var closer io.Closer

	useGzip := strings.HasSuffix(archiveFilename, ".gz") || strings.HasSuffix(archiveFilename, ".tgz")

	if useGzip {
		gw := gzip.NewWriter(buf)
		tw = tar.NewWriter(gw)
		closer = gw
	} else {
		tw = tar.NewWriter(buf)
		closer = tw // tar.Writer also needs to be Close()d
	}
	defer func() {
		if err := closer.Close(); err != nil {
			t.Logf("Failed to close writer: %v", err)
		}
	}() // Close either gzip.Writer or tar.Writer
	if tw != closer { // If using gzip, close tar.Writer separately
		defer func() {
			if err := tw.Close(); err != nil {
				t.Logf("Failed to close tar writer: %v", err)
			}
		}()
	}

	now := time.Now()
	// Add directories
	for _, dir := range dirs {
		name := strings.TrimSuffix(dir, "/") + "/"
		hdr := &tar.Header{
			Name:     name,
			Mode:     0755,
			Typeflag: tar.TypeDir,
			ModTime:  now,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("Failed to write tar header for directory %s: %v", name, err)
		}
	}

	// Add files
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Size:     int64(len(content)),
			Mode:     0644,
			Typeflag: tar.TypeReg,
			ModTime:  now,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("Failed to write tar header for file %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("Failed to write content for file %s: %v", name, err)
		}
	}
	return buf
}

func TestUploadHandler_Success_Tar(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-tar-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
		}
	}()

	filesToArchive := map[string]string{
		"file1.txt":        "content of file1",
		"subdir/file2.txt": "content of file2",
	}
	dirsToArchive := []string{"subdir/"}
	archiveName := "test.tar"
	archiveContent := createTestArchive(t, filesToArchive, dirsToArchive, archiveName)

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", archiveName)
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	err = writer.WriteField("path", tempDir)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		// Expect JSON response: {"message":"Archive extracted successfully to /path","path":"/path"}
		var resp map[string]string
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.NoError(t, err, "Failed to parse JSON response")
		assert.Contains(t, resp["message"], "Archive extracted successfully", "Success message mismatch")
		assert.NotEmpty(t, resp["path"], "Path should be in response")
	}

	// Verify extracted files
	content1, err := os.ReadFile(filepath.Join(tempDir, "file1.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "content of file1", string(content1))

	content2, err := os.ReadFile(filepath.Join(tempDir, "subdir/file2.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "content of file2", string(content2))

	_, err = os.Stat(filepath.Join(tempDir, "subdir"))
	assert.NoError(t, err, "Subdirectory should exist")
}

func TestUploadHandler_Success_TarGz(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-tgz-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
		}
	}()

	filesToArchive := map[string]string{
		"fileA.txt":        "content of fileA",
		"folder/fileB.log": "log content",
	}
	dirsToArchive := []string{"folder/"}
	archiveName := "test.tar.gz" // Test with .tar.gz
	archiveContent := createTestArchive(t, filesToArchive, dirsToArchive, archiveName)

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", archiveName)
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	err = writer.WriteField("path", tempDir)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.NoError(t, err, "Failed to parse JSON response")
		assert.Contains(t, resp["message"], "Archive extracted successfully", "Success message mismatch")
		assert.NotEmpty(t, resp["path"], "Path should be in response")
	}

	contentA, err := os.ReadFile(filepath.Join(tempDir, "fileA.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "content of fileA", string(contentA))

	contentB, err := os.ReadFile(filepath.Join(tempDir, "folder/fileB.log"))
	assert.NoError(t, err)
	assert.Equal(t, "log content", string(contentB))
}

func TestUploadHandler_NoPath(t *testing.T) {
	e := echo.New()

	archiveContent := createTestArchive(t, map[string]string{"dummy.txt": "data"}, nil, "dummy.tar.gz")
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", "dummy.tar.gz")
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	// No "path" field
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	// Call the handler and expect an error
	_ = UploadHandler(c) // The error is checked by the response code and body
	// assert.Error(t, err) // c.Error() makes the handler return nil, error is checked by status code

	// Assert the HTTP status code and response body
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "{\"error\":\"Destination directory not specified\"}\n", rec.Body.String()) // Key is "error"
}

func TestUploadHandler_NoTarfile(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-notar-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
		}
	}()

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	// No "tarfile" field
	err = writer.WriteField("path", tempDir)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	_ = UploadHandler(c) // The error is checked by the response code and body
	// assert.Error(t, err) // c.Error() makes the handler return nil, error is checked by status code

	// Assert the HTTP status code and response body
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]string
	errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, errUnmarshal, "Failed to parse JSON error response for NoTarfile")
	assert.Contains(t, resp["error"], "File not found in request", "Expected 'File not found in request' error message")
}

func TestUploadHandler_InvalidGzip(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-badgzip-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
		}
	}()

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", "invalid.tar.gz") // .gz extension but not a gzip file
	assert.NoError(t, err)
	_, err = part.Write([]byte("this is not a valid gzip content"))
	assert.NoError(t, err)
	err = writer.WriteField("path", tempDir)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	_ = UploadHandler(c) // The error is checked by the response code and body
	// assert.Error(t, err) // c.Error() makes the handler return nil, error is checked by status code

	// Assert the HTTP status code and response body
	assert.Equal(t, http.StatusBadRequest, rec.Code) // Service returns 400 for this
	// Service returns more detailed error: "failed to create gzip reader for archive 'invalid.tar.gz': gzip: invalid header"
	var resp map[string]string
	err = json.Unmarshal(rec.Body.Bytes(), &resp) // Use = instead of :=
	assert.NoError(t, err, "Failed to parse JSON error response")
	assert.Contains(t, resp["error"], "failed to create gzip reader", "Error message mismatch")
	assert.Contains(t, resp["error"], "gzip: invalid header", "Error message detail mismatch")
}

func TestUploadHandler_PathTraversalAttempt(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-traversal-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
		}
	}()

	// Create a tar with a path traversal attempt
	// Note: filepath.Join on the server will clean this, but the check is for after cleaning
	filesToArchive := map[string]string{"../../evil.txt": "evil content"}
	archiveName := "traversal.tar.gz"
	archiveContent := createTestArchive(t, filesToArchive, nil, archiveName)

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", archiveName)
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	err = writer.WriteField("path", tempDir)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	// Call the handler and expect an error
	_ = UploadHandler(c)
	// assert.Error(t, err) // c.Error() makes the handler return nil, error is checked by status code

	// Assert the HTTP status code and response body
	assert.Equal(t, http.StatusForbidden, rec.Code) // Service returns 403
	var resp map[string]string
	err = json.Unmarshal(rec.Body.Bytes(), &resp) // Use = instead of :=
	assert.NoError(t, err, "Failed to parse JSON error response")
	// Service error: "tar archive 'traversal.tar.gz' contains potentially unsafe path entry '../../evil.txt'"
	assert.Contains(t, resp["error"], "contains potentially unsafe path entry", "Error message mismatch")

	_, err = os.Stat(filepath.Join(tempDir, "evil.txt")) // Check inside tempDir
	assert.True(t, os.IsNotExist(err), "File should not be created inside tempDir due to path cleaning before check")
}

func TestUploadHandler_WithPathPrefix_AllowedPath(t *testing.T) {
	e := echo.New()
	// Use an absolute path for prefix in test environment for clarity
	baseDirForPrefixTest, err := os.MkdirTemp("", "prefix_base_")
	require.NoError(t, err)
	defer os.RemoveAll(baseDirForPrefixTest)
	pathPrefix := filepath.Join(baseDirForPrefixTest, "allowed", "prefix")
	err = os.MkdirAll(pathPrefix, 0755) // Ensure prefix directory actually exists itself
	require.NoError(t, err)

	if err := os.Setenv("PATH_PREFIX", pathPrefix); err != nil {
		t.Fatalf("failed to set env PATH_PREFIX: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("PATH_PREFIX"); err != nil {
			t.Fatalf("failed to unset env PATH_PREFIX: %v", err)
		}
	}()

	// tempDir is not directly used for formPath construction here, path is relative to prefix
	// formPath := filepath.Join(tempDir, pathPrefix, "data") // Old logic
	// err = os.MkdirAll(filepath.Dir(formPath), 0755) // Old logic
	// assert.NoError(t, err)

	// defer func() { // tempDir might not be needed if formPath is relative
	// 	 if err := os.RemoveAll(tempDir); err != nil {
	// 		 t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
	// 	 }
	// }()

	filesToArchive := map[string]string{
		"file1.txt":        "content of file1",
		"subdir/file2.txt": "content of file2",
	}
	dirsToArchive := []string{"subdir/"}
	archiveName := "test_allowed.tar.gz"
	archiveContent := createTestArchive(t, filesToArchive, dirsToArchive, archiveName)

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", archiveName)
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	// The 'path' field in the form should be relative to the PATH_PREFIX.
	formPathValue := "data"
	err = writer.WriteField("path", formPathValue)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.NoError(t, err, "Failed to parse JSON response")
		assert.Contains(t, resp["message"], "Archive extracted successfully", "Success message mismatch")
	}

	// Verify extracted files in the target directory (pathPrefix + formPathValue)
	absExtractPath := filepath.Join(pathPrefix, formPathValue)
	content1, err := os.ReadFile(filepath.Join(absExtractPath, "file1.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "content of file1", string(content1))

	content2, err := os.ReadFile(filepath.Join(absExtractPath, "subdir/file2.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "content of file2", string(content2))

	_, err = os.Stat(filepath.Join(absExtractPath, "subdir"))
	assert.NoError(t, err, "Subdirectory should exist in the target path")
}

func TestUploadHandler_WithPathPrefix_PathExactlyPrefix(t *testing.T) {
	e := echo.New()
	baseDirForPrefixTest, err := os.MkdirTemp("", "exact_prefix_base_")
	require.NoError(t, err)
	defer os.RemoveAll(baseDirForPrefixTest)
	pathPrefix := filepath.Join(baseDirForPrefixTest, "allowed", "exact_prefix")
	err = os.MkdirAll(pathPrefix, 0755)  // Ensure prefix directory actually exists
	require.NoError(t, err)

	if err := os.Setenv("PATH_PREFIX", pathPrefix); err != nil {
		t.Fatalf("failed to set env PATH_PREFIX: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("PATH_PREFIX"); err != nil {
			t.Fatalf("failed to unset env PATH_PREFIX: %v", err)
		}
	}()

	// pathPrefix itself is the target for extraction.

	filesToArchive := map[string]string{
		"rootfile.txt": "content at root of archive",
	}
	archiveName := "test_exact_match.tar"
	archiveContent := createTestArchive(t, filesToArchive, nil, archiveName)

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", archiveName)
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	// User specifies "" as the path, meaning "root of prefix".
	err = writer.WriteField("path", "")
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.NoError(t, err, "Failed to parse JSON response")
		assert.Contains(t, resp["message"], "Archive extracted successfully", "Success message mismatch")
	}

	// Verify extracted file in the pathPrefix directory
	content, err := os.ReadFile(filepath.Join(pathPrefix, "rootfile.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "content at root of archive", string(content))
}

func TestUploadHandler_WithPathPrefix_DisallowedPath(t *testing.T) {
	e := echo.New()
	baseDirForPrefixTest, err := os.MkdirTemp("", "disallowed_prefix_base_")
	require.NoError(t, err) // This require is testify/require
	defer os.RemoveAll(baseDirForPrefixTest)
	pathPrefix := filepath.Join(baseDirForPrefixTest, "allowed", "prefix")
	err = os.MkdirAll(pathPrefix, 0755) // Ensure prefix directory actually exists for validation logic
	require.NoError(t, err) // This require is testify/require

	if err := os.Setenv("PATH_PREFIX", pathPrefix); err != nil {
		t.Fatalf("failed to set env PATH_PREFIX: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("PATH_PREFIX"); err != nil {
			t.Fatalf("failed to unset env PATH_PREFIX: %v", err)
		}
	}()

	tempDir, err := os.MkdirTemp("", "test-deploy-tar-prefix-disallowed-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
		}
	}()

	// This path does not start with PATH_PREFIX
	disallowedPath := filepath.Join(tempDir, "disallowed", "path")
	// Create the directory, as the handler might attempt to access it before validation failure in some scenarios,
	// though for prefix validation, it should fail before filesystem access for extraction.
	err = os.MkdirAll(disallowedPath, 0755)
	assert.NoError(t, err)

	archiveContent := createTestArchive(t, map[string]string{"dummy.txt": "data"}, nil, "dummy.tar")

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", "dummy.tar")
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	err = writer.WriteField("path", disallowedPath) // Path that does NOT start with PATH_PREFIX
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	err = UploadHandler(c) // Expect an error handled by Echo's HTTPErrorHandler

	// Check if the error is the specific one we expect from path validation
	// Service layer will return a specific error, which handler maps.
	// Expecting 403 Forbidden due to path being outside prefix.
	// The error message will come from service.UploadFile's validation.
	assert.Equal(t, http.StatusForbidden, rec.Code)
	var resp map[string]string
	errJSON := json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, errJSON, "Failed to parse JSON error response")
	assert.Contains(t, resp["error"], "is outside the scope of path prefix", "Error message mismatch")


	// Ensure no files were extracted
	_, statErr := os.Stat(filepath.Join(disallowedPath, "dummy.txt"))
	assert.True(t, os.IsNotExist(statErr), "File should not be extracted to a disallowed path")
}

func TestUploadHandler_Success_Put_Overwrites(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-put-overwrite-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
		}
	}()

	// 1. Create an initial file in the tempDir
	oldFilePath := filepath.Join(tempDir, "old_file.txt")
	oldFileContent := []byte("this is the old content")
	err = os.WriteFile(oldFilePath, oldFileContent, 0644)
	assert.NoError(t, err)

	// 2. Create a new tar archive with a new file
	filesToArchive := map[string]string{
		"new_file.txt": "this is the new content",
	}
	archiveName := "new_archive.tar"
	archiveContent := createTestArchive(t, filesToArchive, nil, archiveName)

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", archiveName)
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	err = writer.WriteField("path", tempDir)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/", body) // Use PUT method
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.NoError(t, err, "Failed to parse JSON response")
		assert.Contains(t, resp["message"], "Archive extracted successfully", "Success message mismatch")
	}

	// 3. Assertions
	//    - old_file.txt should not exist
	_, err = os.Stat(oldFilePath)
	assert.True(t, os.IsNotExist(err), "Old file should have been removed by PUT operation")

	//    - new_file.txt should exist and have correct content
	newFilePath := filepath.Join(tempDir, "new_file.txt")
	newFileContent, err := os.ReadFile(newFilePath)
	assert.NoError(t, err)
	assert.Equal(t, "this is the new content", string(newFileContent))
}

func TestUploadHandler_Success_Put_WithPathPrefix_AllowedPath(t *testing.T) {
	e := echo.New()
	baseDirForPrefixTest, err := os.MkdirTemp("", "put_prefix_base_")
	require.NoError(t, err)
	defer os.RemoveAll(baseDirForPrefixTest)
	pathPrefix := filepath.Join(baseDirForPrefixTest, "allowed", "put_prefix")
	err = os.MkdirAll(pathPrefix, 0755)  // Ensure prefix directory itself exists
	require.NoError(t, err)

	if err := os.Setenv("PATH_PREFIX", pathPrefix); err != nil {
		t.Fatalf("failed to set env PATH_PREFIX: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("PATH_PREFIX"); err != nil {
			t.Fatalf("failed to unset env PATH_PREFIX: %v", err)
		}
	}()

	// The path for upload will be relative to pathPrefix
	targetSubDirForPut := "data_put"
	absPathForOldFileSetup := filepath.Join(pathPrefix, targetSubDirForPut) // This is where files will be extracted and where old files are
	err = os.MkdirAll(absPathForOldFileSetup, 0755)
	require.NoError(t, err)


	// 1. Create an initial file in the directory that will be targeted by PUT
	oldFilePath := filepath.Join(absPathForOldFileSetup, "old_prefixed_file.txt")
	oldFileContent := []byte("old content in prefixed path")
	err = os.WriteFile(oldFilePath, oldFileContent, 0644)
	assert.NoError(t, err)

	// 2. Create a new tar archive with new files
	filesToArchive := map[string]string{
		"new_prefixed_file.txt":       "new content for prefixed path",
		"subdir_put/another_file.txt": "another new file",
	}
	dirsToArchive := []string{"subdir_put/"}
	archiveName := "test_put_allowed.tar.gz"
	archiveContent := createTestArchive(t, filesToArchive, dirsToArchive, archiveName)

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", archiveName)
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	err = writer.WriteField("path", targetSubDirForPut) // Relative path to prefix
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/", body) // Use PUT method
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.NoError(t, err, "Failed to parse JSON response")
		assert.Contains(t, resp["message"], "Archive extracted successfully", "Success message mismatch")
	}

	// 3. Assertions
	//    - old_prefixed_file.txt should not exist in absPathForOldFileSetup
	_, err = os.Stat(oldFilePath)
	assert.True(t, os.IsNotExist(err), "Old file in prefixed path should have been removed by PUT operation")

	//    - new_prefixed_file.txt should exist and have correct content in absPathForOldFileSetup
	newFilePath := filepath.Join(absPathForOldFileSetup, "new_prefixed_file.txt")
	newFileContent, err := os.ReadFile(newFilePath)
	assert.NoError(t, err)
	assert.Equal(t, "new content for prefixed path", string(newFileContent))

	//    - subdir_put/another_file.txt should exist and have correct content in absPathForOldFileSetup
	anotherNewFilePath := filepath.Join(absPathForOldFileSetup, "subdir_put/another_file.txt")
	anotherNewFileContent, err := os.ReadFile(anotherNewFilePath)
	assert.NoError(t, err)
	assert.Equal(t, "another new file", string(anotherNewFileContent))

	_, err = os.Stat(filepath.Join(absPathForOldFileSetup, "subdir_put"))
	assert.NoError(t, err, "New subdirectory 'subdir_put' should exist in the target path")
}

func TestUploadHandler_Success_Post_NonArchiveFile(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-post-file-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
		}
	}()

	fileName := "test.txt"
	fileContent := "this is a test file content for POST"

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", fileName) // "tarfile" is the expected form field name
	assert.NoError(t, err)
	_, err = io.WriteString(part, fileContent)
	assert.NoError(t, err)
	err = writer.WriteField("path", tempDir)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.NoError(t, err, "Failed to parse JSON response")
		assert.Contains(t, resp["message"], "File uploaded successfully", "Success message mismatch")
	}

	// Verify uploaded file
	uploadedFilePath := filepath.Join(tempDir, fileName)
	content, err := os.ReadFile(uploadedFilePath)
	assert.NoError(t, err)
	assert.Equal(t, fileContent, string(content))
}

func TestUploadHandler_Success_Put_NonArchiveFile_Overwrites(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-put-file-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", tempDir, err)
		}
	}()

	// 1. Create an initial file in the tempDir
	oldFileName := "old_file.txt"
	oldFilePath := filepath.Join(tempDir, oldFileName)
	oldFileContent := []byte("this is the old content")
	err = os.WriteFile(oldFilePath, oldFileContent, 0644)
	assert.NoError(t, err)

	// 2. Prepare new file for PUT upload
	newFileName := "new_file.txt"
	newFileContent := "this is the new file content for PUT"

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", newFileName) // "tarfile" is the expected form field name
	assert.NoError(t, err)
	_, err = io.WriteString(part, newFileContent)
	assert.NoError(t, err)
	err = writer.WriteField("path", tempDir)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPut, "/", body) // Use PUT method
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		var resp map[string]string
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		assert.NoError(t, err, "Failed to parse JSON response")
		assert.Contains(t, resp["message"], "File uploaded successfully", "Success message mismatch")
	}

	// 3. Assertions
	//    - old_file.txt should not exist
	_, err = os.Stat(oldFilePath)
	assert.True(t, os.IsNotExist(err), "Old file should have been removed by PUT operation")

	//    - new_file.txt should exist and have correct content
	uploadedFilePath := filepath.Join(tempDir, newFileName)
	content, err := os.ReadFile(uploadedFilePath)
	assert.NoError(t, err)
	assert.Equal(t, newFileContent, string(content))
}
