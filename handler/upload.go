package handler

import (
	"deploytar/service"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

func UploadHandler(c echo.Context) error {
	pathPrefixEnv := os.Getenv("PATH_PREFIX")
	baseDirPath := c.FormValue("path")

	if baseDirPath == "" && pathPrefixEnv == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Destination directory not specified"})
	}

	fileHeader, err := c.FormFile("tarfile")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "File not found in request: " + err.Error()})
	}

	src, err := fileHeader.Open()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to open uploaded file"})
	}
	defer func() {
		if err := src.Close(); err != nil {
			_ = err
		}
	}()

	isPutRequest := c.Request().Method == http.MethodPut

	// PATH_PREFIXが設定されている場合、空文字列はプレフィックス直下を意味する
	targetPath := baseDirPath
	if baseDirPath == "" && pathPrefixEnv != "" {
		targetPath = "."
	}

	finalPath, err := service.UploadFile(src, targetPath, fileHeader.Filename, pathPrefixEnv, isPutRequest)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "forbidden") ||
			strings.Contains(errMsg, "traversal") ||
			strings.Contains(errMsg, "outside the scope") ||
			strings.Contains(errMsg, "unsafe path") ||
			strings.Contains(errMsg, "cannot be a path traversal attempt") {
			return c.JSON(http.StatusForbidden, map[string]string{"error": errMsg})
		}
		if strings.Contains(errMsg, "not found") ||
			strings.Contains(errMsg, "does not exist") {
			return c.JSON(http.StatusNotFound, map[string]string{"error": errMsg})
		}
		if strings.Contains(errMsg, "archive") ||
			strings.Contains(errMsg, "gzipped content") ||
			strings.Contains(errMsg, "file content") ||
			strings.Contains(errMsg, "is not a directory") {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": errMsg})
		}

		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to process file upload"})
	}

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
