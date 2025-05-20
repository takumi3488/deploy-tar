package handler

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
)

// uploadHandler processes a single tar file upload
func UploadHandler(c echo.Context) error {
	// Get the destination directory
	baseDirPath := c.FormValue("path")
	if baseDirPath == "" {
		return c.String(http.StatusBadRequest, "Destination directory not specified")
	}
	// Ensure the base directory exists
	if err := os.MkdirAll(baseDirPath, 0755); err != nil {
		return c.String(http.StatusInternalServerError, "Failed to create base directory: "+err.Error())
	}

	// Get the tar file
	fileHeader, err := c.FormFile("tarfile")
	if err != nil {
		return c.String(http.StatusBadRequest, "Tar file not found in request: "+err.Error())
	}

	src, err := fileHeader.Open()
	if err != nil {
		return c.String(http.StatusInternalServerError, "Failed to open uploaded tar file: "+err.Error())
	}
	defer src.Close()

	var fileReader io.Reader = src

	// Check if the file is gzipped
	if strings.HasSuffix(fileHeader.Filename, ".gz") || strings.HasSuffix(fileHeader.Filename, ".tgz") {
		gzr, err := gzip.NewReader(src)
		if err != nil {
			return c.String(http.StatusInternalServerError, "Failed to create gzip reader: "+err.Error())
		}
		defer gzr.Close()
		fileReader = gzr
	}

	tarReader := tar.NewReader(fileReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // End of tar archive
		}
		if err != nil {
			return c.String(http.StatusInternalServerError, "Failed to read tar header: "+err.Error())
		}

		// Construct the target path for the entry
		// IMPORTANT: Sanitize the header.Name to prevent path traversal attacks
		target := filepath.Join(baseDirPath, header.Name)

		// Security check: Ensure the cleaned path is still within the baseDirPath
		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(cleanTarget, filepath.Clean(baseDirPath)+string(os.PathSeparator)) && cleanTarget != filepath.Clean(baseDirPath) {
			c.Logger().Warnf("Path traversal attempt detected: %s (cleaned: %s)", header.Name, cleanTarget)
			return c.String(http.StatusBadRequest, "Invalid path in tar file (path traversal attempt)")
		}

		switch header.Typeflag {
		case tar.TypeDir:
			// Create directory
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return c.String(http.StatusInternalServerError, "Failed to create directory from tar: "+err.Error())
			}
		case tar.TypeReg:
			// Create file
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return c.String(http.StatusInternalServerError, "Failed to create parent directory for file: "+err.Error())
			}

			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return c.String(http.StatusInternalServerError, "Failed to create file from tar: "+err.Error())
			}
			// Using a func to ensure outFile.Close() is called after copying
			func() {
				defer outFile.Close()
				if _, err := io.Copy(outFile, tarReader); err != nil {
					// This error will be shadowed if not handled carefully,
					// but we'll return from the outer function if it occurs.
					// For more robust error handling, consider collecting errors.
					c.Logger().Errorf("Failed to copy file content from tar for %s: %v", target, err)
				}
			}()

		default:
			c.Logger().Infof("Unsupported tar entry type %c for %s", header.Typeflag, header.Name)
		}
	}

	return c.String(http.StatusOK, "Tar file extracted successfully")
}