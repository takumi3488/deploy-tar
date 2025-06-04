package handler

import (
	"deploytar/service"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

type DirectoryEntry struct {
	Name string  `json:"name"`
	Type string  `json:"type"`
	Size *string `json:"size,omitempty"`
	Link string  `json:"link"`
}

type DirectoryResponse struct {
	Path       string           `json:"path"`
	Entries    []DirectoryEntry `json:"entries"`
	ParentLink *string          `json:"parent_link,omitempty"`
}

func ListDirectoryHandler(c echo.Context) error {
	rawQuerySubDir := c.QueryParam("d")
	pathPrefixEnv := os.Getenv("PATH_PREFIX")

	validatedAbsPath, displayPathFromService, err := service.ResolveAndValidatePath(rawQuerySubDir, pathPrefixEnv)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		if strings.Contains(err.Error(), "is not a directory") {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if strings.Contains(err.Error(), "forbidden") ||
			strings.Contains(err.Error(), "traversal") ||
			strings.Contains(err.Error(), "outside its allowed scope") ||
			strings.Contains(err.Error(), "outside CWD") ||
			strings.Contains(err.Error(), "outside prefix") {
			return c.JSON(http.StatusForbidden, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Internal server error during path validation"})
	}

	serviceEntries, serviceParentLink, err := service.ListDirectory(validatedAbsPath, rawQuerySubDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("Directory not found: %s", displayPathFromService)})
		}
		if errors.Is(err, os.ErrPermission) {
			return c.JSON(http.StatusForbidden, map[string]string{"error": fmt.Sprintf("Permission denied for directory: %s", displayPathFromService)})
		}
		if strings.Contains(err.Error(), "failed to read directory") {
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file or directory") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("Directory not found: %s", displayPathFromService)})
			}
			if strings.Contains(err.Error(), "permission denied") {
				return c.JSON(http.StatusForbidden, map[string]string{"error": fmt.Sprintf("Permission denied for directory: %s", displayPathFromService)})
			}
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to read directory contents"})
	}

	var entries []DirectoryEntry
	for _, se := range serviceEntries {
		entry := DirectoryEntry{
			Name: se.Name,
			Type: se.Type,
			Link: fmt.Sprintf("/list?d=%s", url.QueryEscape(se.Link)),
		}
		if se.Size != "" {
			entry.Size = &se.Size
		}
		entries = append(entries, entry)
	}

	var parentLinkResponse *string
	if serviceParentLink != "" {
		var formattedParent string
		if serviceParentLink == "/" {
			formattedParent = "/list?d=/"
		} else {
			formattedParent = fmt.Sprintf("/list?d=%s", url.QueryEscape(serviceParentLink))
		}
		parentLinkResponse = &formattedParent
	}

	response := DirectoryResponse{
		Path:       displayPathFromService,
		Entries:    entries,
		ParentLink: parentLinkResponse,
	}
	return c.JSON(http.StatusOK, response)
}
