package handler

import (
	"deploytar/service" // Assuming 'deploytar' is the module name
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

// UploadHandler handles file uploads. Supports plain files, .tar, .tar.gz, .tgz, and .gz.
// It can behave like a PUT request if the method is PUT, clearing the target directory first.
func UploadHandler(c echo.Context) error {
	pathPrefixEnv := os.Getenv("PATH_PREFIX")
	baseDirPath := c.FormValue("path") // User-provided target directory path relative to prefix or CWD

	if baseDirPath == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Destination directory not specified"})
	}

	fileHeader, err := c.FormFile("tarfile")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "File not found in request: " + err.Error()})
	}

	src, err := fileHeader.Open()
	if err != nil {
		c.Logger().Errorf("Failed to open uploaded file header: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to open uploaded file"})
	}
	defer src.Close()

	isPutRequest := c.Request().Method == http.MethodPut

	// Call the service layer for file upload
	finalPath, err := service.UploadFile(src, baseDirPath, fileHeader.Filename, pathPrefixEnv, isPutRequest)
	if err != nil {
		// Basic error mapping; can be more granular with custom service errors
		errMsg := err.Error()
		// Check for specific error messages from the service layer to map to appropriate HTTP status codes
		if strings.Contains(errMsg, "forbidden") ||
			strings.Contains(errMsg, "traversal") ||
			strings.Contains(errMsg, "outside the scope") ||
			strings.Contains(errMsg, "unsafe path") ||
			strings.Contains(errMsg, "cannot be a path traversal attempt") {
			c.Logger().Warnf("Upload forbidden: %v (user path: %s, filename: %s, prefix: %s)", err, baseDirPath, fileHeader.Filename, pathPrefixEnv)
			return c.JSON(http.StatusForbidden, map[string]string{"error": errMsg})
		}
		if strings.Contains(errMsg, "not found") ||
			strings.Contains(errMsg, "does not exist") { // e.g. PATH_PREFIX dir not found
			c.Logger().Infof("Upload target or prefix not found: %v", err)
			return c.JSON(http.StatusNotFound, map[string]string{"error": errMsg})
		}
		if strings.Contains(errMsg, "archive") || // Covers tar/gzip read issues
			strings.Contains(errMsg, "gzipped content") || // Covers bad .gz file
			strings.Contains(errMsg, "file content") || // Covers io.Copy issues for plain files
			strings.Contains(errMsg, "is not a directory") { // e.g. PATH_PREFIX is a file
			c.Logger().Warnf("Bad request during upload: %v", err)
			return c.JSON(http.StatusBadRequest, map[string]string{"error": errMsg})
		}

		// Default to InternalServerError for other errors
		c.Logger().Errorf("Service UploadFile error: %v (user path: %s, filename: %s, prefix: %s)", err, baseDirPath, fileHeader.Filename, pathPrefixEnv)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to process file upload"})
	}

	// Success message construction
	var message string
	fileNameLower := strings.ToLower(fileHeader.Filename)
	if strings.HasSuffix(fileNameLower, ".tar") || strings.HasSuffix(fileNameLower, ".tgz") || strings.HasSuffix(fileNameLower, ".tar.gz") {
		message = fmt.Sprintf("Archive extracted successfully to %s", finalPath)
	} else if strings.HasSuffix(fileNameLower, ".gz") {
		message = fmt.Sprintf("File decompressed and saved to %s", finalPath)
	} else {
		message = fmt.Sprintf("File uploaded successfully to %s", finalPath)
	}

	return c.JSON(http.StatusOK, map[string]string{"message": message, "path": finalPath})
}
