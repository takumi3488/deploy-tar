package handler

import (
	"deploytar/service" // Assuming 'deploytar' is the module name
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
)

// DirectoryEntry represents a file or directory in the listing.
type DirectoryEntry struct {
	Name string  `json:"name"`
	Type string  `json:"type"` // "file" or "directory"
	Size *string `json:"size,omitempty"`
	Link string  `json:"link"`
}

// DirectoryResponse is the structure for the JSON response.
type DirectoryResponse struct {
	Path       string            `json:"path"`
	Entries    []DirectoryEntry  `json:"entries"`
	ParentLink *string           `json:"parent_link,omitempty"`
}

func ListDirectoryHandler(c echo.Context) error {
	rawQuerySubDir := c.QueryParam("d")
	pathPrefixEnv := os.Getenv("PATH_PREFIX")

	// Call the service layer for path validation
	validatedAbsPath, displayPathFromService, err := service.ResolveAndValidatePath(rawQuerySubDir, pathPrefixEnv)
	if err != nil {
		// Map service errors to HTTP errors
		if strings.Contains(err.Error(), "not found") { // e.g. PATH_PREFIX not found, or path itself
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		if strings.Contains(err.Error(), "is not a directory") { // e.g. PATH_PREFIX not a dir
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		// "forbidden" or "traversal" or "outside its allowed scope" or "outside CWD" or "outside prefix"
		if strings.Contains(err.Error(), "forbidden") ||
			strings.Contains(err.Error(), "traversal") ||
			strings.Contains(err.Error(), "outside its allowed scope") ||
			strings.Contains(err.Error(), "outside CWD") ||
			strings.Contains(err.Error(), "outside prefix") {
			return c.JSON(http.StatusForbidden, map[string]string{"error": err.Error()})
		}
		c.Logger().Errorf("Path validation error: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Internal server error during path validation"})
	}

	// Call the service layer for directory listing
	// Pass rawQuerySubDir as originalRequestPath for link generation consistency.
	serviceEntries, serviceParentLink, err := service.ListDirectory(validatedAbsPath, rawQuerySubDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("Directory not found: %s", displayPathFromService)})
		}
		if errors.Is(err, os.ErrPermission) {
			return c.JSON(http.StatusForbidden, map[string]string{"error": fmt.Sprintf("Permission denied for directory: %s", displayPathFromService)})
		}
		// Check for specific wrapped error text if errors.Is doesn't catch generic os errors fully
		if strings.Contains(err.Error(), "failed to read directory") { // Generic from service
             if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file or directory") {
                return c.JSON(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("Directory not found: %s", displayPathFromService)})
             }
             if strings.Contains(err.Error(), "permission denied") {
                 return c.JSON(http.StatusForbidden, map[string]string{"error": fmt.Sprintf("Permission denied for directory: %s", displayPathFromService)})
             }
        }
		c.Logger().Errorf("Service ListDirectory error: %v for path %s (validated %s)", err, rawQuerySubDir, validatedAbsPath)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to read directory contents"})
	}

	// Adapt service response to handler's DirectoryResponse
	var entries []DirectoryEntry
	for _, se := range serviceEntries {
		entry := DirectoryEntry{
			Name: se.Name,
			Type: se.Type,
			// service.ListDirectory returns Link as a path relative to the "current view" for client navigation
			Link: fmt.Sprintf("/list?d=%s", url.QueryEscape(se.Link)),
		}
		if se.Size != "" { // Size is already formatted string from service
			entry.Size = &se.Size
		}
		entries = append(entries, entry)
	}

	var parentLinkResponse *string
	if serviceParentLink != "" {
		// serviceParentLink from service is like "/", or "some/path", or ""
		var formattedParent string
		if serviceParentLink == "/" {
			formattedParent = "/list?d=/"
		} else { // For other non-empty paths like "some/path"
			formattedParent = fmt.Sprintf("/list?d=%s", url.QueryEscape(serviceParentLink))
		}
		parentLinkResponse = &formattedParent
	}
	// If serviceParentLink is "", parentLinkResponse remains nil, which is correct (no parent link for root)


	response := DirectoryResponse{
		Path:       displayPathFromService, // Use the display path from ResolveAndValidatePath
		Entries:    entries,
		ParentLink: parentLinkResponse,
	}
	return c.JSON(http.StatusOK, response)
}
