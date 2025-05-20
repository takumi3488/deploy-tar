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
	defer closer.Close() // Close either gzip.Writer or tar.Writer
	if tw != closer {    // If using gzip, close tar.Writer separately
		defer tw.Close()
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
	defer os.RemoveAll(tempDir)

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
	defer os.RemoveAll(tempDir)

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
	// We expect an error to be returned from the handler, so don't check NoError
	err = UploadHandler(c) // Ignore the error and validate the response code and body
	assert.Error(t, err)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "Destination directory not specified", rec.Body.String())
}

func TestUploadHandler_NoTarfile(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-notar-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

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
	err = UploadHandler(c)
	assert.Error(t, err)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Body.String(), "Tar file not found in request"), "Expected tar file not found error")
}

func TestUploadHandler_InvalidGzip(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-badgzip-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

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
	err = UploadHandler(c)
	assert.Error(t, err)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "Failed to create gzip reader: gzip: invalid header", rec.Body.String())
}

func TestUploadHandler_PathTraversalAttempt(t *testing.T) {
	e := echo.New()

	tempDir, err := os.MkdirTemp("", "test-deploy-traversal-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

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
	err = UploadHandler(c) // Ignore the error and validate the response code and body
	assert.Error(t, err)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "Invalid path in tar file (path traversal attempt)", rec.Body.String())

	_, err = os.Stat(filepath.Join(tempDir, "evil.txt")) // Check inside tempDir
	assert.True(t, os.IsNotExist(err), "File should not be created inside tempDir due to path cleaning before check")
}
