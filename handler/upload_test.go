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

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
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
		assert.Equal(t, "Tar file extracted successfully", rec.Body.String())
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
		assert.Equal(t, "Tar file extracted successfully", rec.Body.String())
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
	assert.Equal(t, "{\"message\":\"Destination directory not specified\"}\n", rec.Body.String())
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
	assert.True(t, strings.HasPrefix(rec.Body.String(), "{\"message\":\"Tar file not found in request"), "Expected 'Tar file not found in request' error message")
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
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "{\"message\":\"Failed to create gzip reader: gzip: invalid header\"}\n", rec.Body.String())
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
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "{\"message\":\"Invalid path in tar file (path traversal attempt)\"}\n", rec.Body.String())

	_, err = os.Stat(filepath.Join(tempDir, "evil.txt")) // Check inside tempDir
	assert.True(t, os.IsNotExist(err), "File should not be created inside tempDir due to path cleaning before check")
}

func TestUploadHandler_WithPathPrefix_AllowedPath(t *testing.T) {
	e := echo.New()
	pathPrefix := "/allowed/prefix"
	if err := os.Setenv("PATH_PREFIX", pathPrefix); err != nil {
		t.Fatalf("failed to set env PATH_PREFIX: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("PATH_PREFIX"); err != nil {
			t.Fatalf("failed to unset env PATH_PREFIX: %v", err)
		}
	}()

	tempDir, err := os.MkdirTemp("", "test-deploy-tar-prefix-*")
	assert.NoError(t, err)

	formPath := filepath.Join(tempDir, pathPrefix, "data") // e.g., /tmp/test-xxxx/allowed/prefix/data
	err = os.MkdirAll(filepath.Dir(formPath), 0755)         // Ensure parent of formPath exists
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
	archiveName := "test_allowed.tar.gz"
	archiveContent := createTestArchive(t, filesToArchive, dirsToArchive, archiveName)

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("tarfile", archiveName)
	assert.NoError(t, err)
	_, err = io.Copy(part, archiveContent)
	assert.NoError(t, err)
	// The 'path' field in the form should be the actual destination path
	// that starts with PATH_PREFIX.
	err = writer.WriteField("path", formPath)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "Tar file extracted successfully", rec.Body.String())
	}

	// Verify extracted files in the target directory (formPath)
	content1, err := os.ReadFile(filepath.Join(formPath, "file1.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "content of file1", string(content1))

	content2, err := os.ReadFile(filepath.Join(formPath, "subdir/file2.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "content of file2", string(content2))

	_, err = os.Stat(filepath.Join(formPath, "subdir"))
	assert.NoError(t, err, "Subdirectory should exist in the target path")
}

func TestUploadHandler_WithPathPrefix_PathExactlyPrefix(t *testing.T) {
	e := echo.New()
	pathPrefix := "/allowed/exact_prefix" // Use a distinct prefix for this test
	if err := os.Setenv("PATH_PREFIX", pathPrefix); err != nil {
		t.Fatalf("failed to set env PATH_PREFIX: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("PATH_PREFIX"); err != nil {
			t.Fatalf("failed to unset env PATH_PREFIX: %v", err)
		}
	}()

	// The 'path' form value will be exactly PATH_PREFIX.
	// For the test, this path must be writable. We use tempDir as a base.
	// So, the formPath will be filepath.Join(tempDir, pathPrefix)
	// This implies PATH_PREFIX is treated as a directory.
	baseDir, err := os.MkdirTemp("", "test-deploy-tar-exact-prefix-base-*")
	assert.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(baseDir); err != nil {
			t.Logf("Failed to remove temp directory %s: %v", baseDir, err)
		}
	}()

	formPath := filepath.Join(baseDir, pathPrefix) // e.g. /tmp/test-base-xxxx/allowed/exact_prefix
	// We need to create `formPath` because files will be extracted *into* it.
	err = os.MkdirAll(formPath, 0755)
	assert.NoError(t, err)


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
	err = writer.WriteField("path", formPath) // Request path is exactly the prefix (made absolute for test)
	assert.NoError(t, err)
	err = writer.Close()
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	c := e.NewContext(req, rec)
	if assert.NoError(t, UploadHandler(c)) {
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "Tar file extracted successfully", rec.Body.String())
	}

	// Verify extracted file in the formPath (which is the prefix itself)
	content, err := os.ReadFile(filepath.Join(formPath, "rootfile.txt"))
	assert.NoError(t, err)
	assert.Equal(t, "content at root of archive", string(content))
}


func TestUploadHandler_WithPathPrefix_DisallowedPath(t *testing.T) {
	e := echo.New()
	if err := os.Setenv("PATH_PREFIX", "/allowed/prefix"); err != nil {
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
	if httpErr, ok := err.(*echo.HTTPError); ok {
		assert.Equal(t, http.StatusBadRequest, httpErr.Code)
		assert.Equal(t, "Path is not allowed", httpErr.Message)
	} else {
		// If no error was returned by the handler (it wrote response directly)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.JSONEq(t, `{"message":"Path is not allowed"}`, rec.Body.String())
	}

	// Ensure no files were extracted
	_, statErr := os.Stat(filepath.Join(disallowedPath, "dummy.txt"))
	assert.True(t, os.IsNotExist(statErr), "File should not be extracted to a disallowed path")
}
