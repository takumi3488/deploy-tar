package handler

import (
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
)

// ListDirectoryHandler is an HTTP handler that lists the contents of a specified directory.
func ListDirectoryHandler(c echo.Context) error {
	pathPrefixEnv := os.Getenv("PATH_PREFIX")
	rawQuerySubDir := c.QueryParam("d") // The original value of ?d= specified by the user

	// 1. Determine cleanedPathPrefix (for path validation and logic branching)
	//    Empty string: No prefix, or effectively points to the root like "/" or "."
	//    Otherwise: Specific prefix like "/serve" or "/app"
	var cleanedPathPrefix string
	if pathPrefixEnv != "" {
		cleanedPathPrefix = filepath.Clean(pathPrefixEnv)
		if cleanedPathPrefix == "." || cleanedPathPrefix == "/" {
			cleanedPathPrefix = "" // Treat root prefix as no prefix
		}
	}

	// 2. Determine effectiveQuerySubDir (for file system access)
	//    If PATH_PREFIX exists and the user specifies d=/, it points to the root of PATH_PREFIX (i.e., specifying a subdirectory is the same as empty)
	effectiveQuerySubDir := rawQuerySubDir
	if cleanedPathPrefix != "" && rawQuerySubDir == "/" {
		effectiveQuerySubDir = ""
	}

	// PRELIMINARY TRAVERSAL CHECK (before os.Stat on prefix or further processing)
	if cleanedPathPrefix != "" {
		cleanedUserRequestPath := filepath.Clean(rawQuerySubDir)

		// If the user's d parameter (cleaned) starts with ".." it's an attempt to go above.
		if strings.HasPrefix(cleanedUserRequestPath, "..") {
			// Test expects "Access to the requested path is forbidden (path traversal attempt?)"
			errToReturn := echo.NewHTTPError(http.StatusForbidden, map[string]string{"error": "Access to the requested path is forbidden (path traversal attempt?)"})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}
		// If the user's d parameter is an absolute path, and it's not simply "/",
		// and it doesn't align with the cleanedPathPrefix, it's suspicious.
		// Note: cleanedUserRequestPath == "/" is allowed as it refers to the root of the prefix.
		if filepath.IsAbs(cleanedUserRequestPath) && cleanedUserRequestPath != "/" {
			// If cleanedPathPrefix is, e.g., "/serve", and user requests d="/etc/passwd",
			// cleanedUserRequestPath will be "/etc/passwd". This should be forbidden.
			// If user requests d="/serve/something", cleanedUserRequestPath is "/serve/something".
			// This check needs to ensure that an absolute path in `d` doesn't bypass the prefix.
			// A simple check: if an absolute d path doesn't start with the prefix, it's an issue.
			if !strings.HasPrefix(cleanedUserRequestPath, cleanedPathPrefix) {
				// Test expects "Access to the requested path is forbidden (path traversal attempt?)"
				errToReturn := echo.NewHTTPError(http.StatusForbidden, map[string]string{"error": "Access to the requested path is forbidden (path traversal attempt?)"})
				c.Echo().HTTPErrorHandler(errToReturn, c)
				return errToReturn
			}
		}
	}
	// END PRELIMINARY TRAVERSAL CHECK

	// 3. Calculate targetDir (for file system access)
	targetFsPath := filepath.Clean(effectiveQuerySubDir)
	if targetFsPath == "" || targetFsPath == "." || targetFsPath == "/" {
		targetFsPath = "." // If empty, ".", or "/", it points to the current directory (.)
	}

	var baseDirForAccess string
	if cleanedPathPrefix != "" {
		prefixInfo, err := os.Stat(cleanedPathPrefix)
		if err != nil {
			if os.IsNotExist(err) {
				errToReturn := echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("Base directory specified by PATH_PREFIX not found: %s", html.EscapeString(cleanedPathPrefix))})
				c.Echo().HTTPErrorHandler(errToReturn, c)
				return errToReturn
			}
			c.Logger().Errorf("Error stating PATH_PREFIX %s: %v", cleanedPathPrefix, err)
			errToReturn := echo.NewHTTPError(http.StatusInternalServerError, map[string]string{"error": "Error accessing base directory path specified by PATH_PREFIX"})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}
		if !prefixInfo.IsDir() {
			errToReturn := echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("Base path specified by PATH_PREFIX is not a directory: %s", html.EscapeString(cleanedPathPrefix))})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}
		baseDirForAccess = cleanedPathPrefix
	} else {
		baseDirForAccess = "." // CWD
	}

	targetDir := filepath.Join(baseDirForAccess, targetFsPath)
	targetDir = filepath.Clean(targetDir)

	// 4. Path validation
	absTargetDir, err := filepath.Abs(targetDir)
	if err != nil {
		c.Logger().Errorf("Failed to get absolute path for targetDir %s: %v", targetDir, err)
		errToReturn := echo.NewHTTPError(http.StatusInternalServerError, map[string]string{"error": "Internal server error during path resolution"})
		c.Echo().HTTPErrorHandler(errToReturn, c)
		return errToReturn
	}

	if cleanedPathPrefix != "" {
		absCleanedPathPrefix, err := filepath.Abs(cleanedPathPrefix)
		if err != nil {
			c.Logger().Errorf("Failed to get absolute path for cleanedPathPrefix %s: %v", cleanedPathPrefix, err)
			errToReturn := echo.NewHTTPError(http.StatusInternalServerError, map[string]string{"error": "Internal server error during path resolution for prefix"})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}

		relPath, err := filepath.Rel(absCleanedPathPrefix, absTargetDir)
		if err != nil {
			c.Logger().Warnf("Path validation failed (filepath.Rel error) for prefix '%s' and target '%s': %v", absCleanedPathPrefix, absTargetDir, err)
			errToReturn := echo.NewHTTPError(http.StatusForbidden, map[string]string{"error": "Access to the requested path is forbidden (path relationship error)"})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}

		if strings.HasPrefix(relPath, "..") || relPath == ".." {
			errToReturn := echo.NewHTTPError(http.StatusForbidden, map[string]string{"error": "Access to the requested path is forbidden (path traversal attempt outside prefix)"})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}
	} else { // If there is no PATH_PREFIX (CWD is the base)
		cwd, err := os.Getwd()
		if err != nil {
			c.Logger().Errorf("Failed to get current working directory: %v", err)
			errToReturn := echo.NewHTTPError(http.StatusInternalServerError, map[string]string{"error": "Internal server error obtaining CWD"})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}
		absCwd, err := filepath.Abs(cwd)
		if err != nil {
			c.Logger().Errorf("Failed to get absolute path for CWD %s: %v", cwd, err)
			errToReturn := echo.NewHTTPError(http.StatusInternalServerError, map[string]string{"error": "Internal server error during CWD path resolution"})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}

		relPath, err := filepath.Rel(absCwd, absTargetDir)
		if err != nil {
			c.Logger().Warnf("Path validation failed (filepath.Rel error) for CWD '%s' and target '%s': %v", absCwd, absTargetDir, err)
			errToReturn := echo.NewHTTPError(http.StatusForbidden, map[string]string{"error": "Access to the requested path is forbidden (path relationship error with CWD)"})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}
		if strings.HasPrefix(relPath, "..") || relPath == ".." {
			errToReturn := echo.NewHTTPError(http.StatusForbidden, map[string]string{"error": "Access to the requested path is forbidden (path traversal attempt outside CWD)"})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}
	}

	// 5. Calculate requestedPathForDisplay (for HTML display, using rawQuerySubDir)
	requestedPathForDisplay := rawQuerySubDir
	// If PATH_PREFIX exists and d=/, it should be displayed as "/" because it's the root of PATH_PREFIX
	// (Since effectiveQuerySubDir is "", targetDir points to ".")
	// In this case, requestedPathForDisplay is treated as an empty string, not "/", and is eventually formatted to "/".
	if cleanedPathPrefix != "" && rawQuerySubDir == "/" {
		requestedPathForDisplay = ""
	}

	if requestedPathForDisplay == "" || requestedPathForDisplay == "." {
		requestedPathForDisplay = "/"
	} else {
		cleanedDisplayPath := filepath.Clean(requestedPathForDisplay)
		if cleanedDisplayPath == "." { // For cases like "dir/.."
			requestedPathForDisplay = "/"
		} else {
			// TrimLeft "/" to avoid something like filepath.Join("/", cleaned)
			requestedPathForDisplay = "/" + strings.TrimLeft(cleanedDisplayPath, "/")
		}
	}

	// Check directory existence and read permission
	dirEntries, err := os.ReadDir(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Use rawQuerySubDir in the error message to match test expectations
			// If rawQuerySubDir is empty or ".", it should be displayed as "/", but
			// in "Directory not found" test cases, rawQuerySubDir usually has a specific name.
			displayErrorPath := rawQuerySubDir
			// Removed empty if block. The commented-out logic is either covered by the initialization of displayErrorPath,
			// or deemed unnecessary in the current code flow.
			errToReturn := echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("Directory not found: %s", html.EscapeString(displayErrorPath))})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}
		if os.IsPermission(err) {
			errToReturn := echo.NewHTTPError(http.StatusForbidden, map[string]string{"error": fmt.Sprintf("Permission denied for directory: %s", html.EscapeString(targetDir))})
			c.Echo().HTTPErrorHandler(errToReturn, c)
			return errToReturn
		}
		c.Logger().Errorf("Error reading directory %s: %v", targetDir, err)
		// Tests expect echo.HTTPError, so match that
		errToReturn := echo.NewHTTPError(http.StatusInternalServerError, map[string]string{"error": "Failed to read directory"})
		c.Echo().HTTPErrorHandler(errToReturn, c)
		return errToReturn
	}

	var htmlBuilder strings.Builder
	htmlBuilder.WriteString("<!DOCTYPE html>\n<html>\n<head>\n")
	htmlBuilder.WriteString(fmt.Sprintf("<title>Index of %s</title>\n", html.EscapeString(requestedPathForDisplay)))
	htmlBuilder.WriteString("<style>body { font-family: sans-serif; } table { border-collapse: collapse; } th, td { padding: 5px; border: 1px solid #ddd; } a { text-decoration: none; color: #007bff; } a:hover { text-decoration: underline; }</style>\n")
	htmlBuilder.WriteString("</head>\n<body>\n")
	htmlBuilder.WriteString(fmt.Sprintf("<h1>Index of %s</h1>\n", html.EscapeString(requestedPathForDisplay)))
	htmlBuilder.WriteString("<table>\n")
	htmlBuilder.WriteString("<tr><th>Name</th><th>Type</th><th>Size</th></tr>\n")

	// Link to parent directory (if not root)
	currentQueryDir := c.QueryParam("d")
	if currentQueryDir != "" && currentQueryDir != "." {
		parentDir := filepath.Dir(currentQueryDir)
		if parentDir == "." { // The parent of the root is the root
			parentDir = ""
		}
		parentLink := "/list"
		if parentDir != "" {
			parentLink = fmt.Sprintf("/list?d=%s", url.QueryEscape(parentDir))
		}
		htmlBuilder.WriteString(fmt.Sprintf("<tr><td><a href=\"%s\">../</a></td><td>Directory</td><td>-</td></tr>\n", parentLink))
	}

	for _, entry := range dirEntries {
		entryName := entry.Name()
		entryType := "File"
		if entry.IsDir() {
			entryType = "Directory"
		}

		// Generate link in new query parameter format
		// If subDir is empty, d=entryName
		// If subDir exists, d=subDir/entryName
		entrySubDir := filepath.Join(c.QueryParam("d"), entryName)
		linkHref := fmt.Sprintf("/list?d=%s", url.QueryEscape(entrySubDir))

		sizeStr := "-"
		if !entry.IsDir() {
			// Consider symbolic links by using getFileInfo
			info, err := getFileInfo(targetDir, entry) // Pass targetDir
			if err == nil {
				sizeStr = formatFileSize(info.Size())
			} else {
				c.Logger().Warnf("Could not get file info for %s: %v", filepath.Join(targetDir, entryName), err)
			}
		}

		if entry.IsDir() {
			htmlBuilder.WriteString(fmt.Sprintf("<tr><td><a href=\"%s\">%s/</a></td><td>%s</td><td>%s</td></tr>\n",
				linkHref, html.EscapeString(entryName), entryType, sizeStr))
		} else {
			// Links to files are intended for download or display, so
			// it's common to link to the actual file path instead of the /list endpoint.
			// However, this handler is for listing only, so either keep it as /list?d=... here,
			// or point to another endpoint (e.g., /files/subDir/filename).
			// The current instruction is to change the listing feature, so keep it as /list?d=...
			// If a file download feature is added in the future, modify this.
			htmlBuilder.WriteString(fmt.Sprintf("<tr><td><a href=\"%s\">%s</a></td><td>%s</td><td>%s</td></tr>\n",
				linkHref, html.EscapeString(entryName), entryType, sizeStr))
		}
	}

	htmlBuilder.WriteString("</table>\n</body>\n</html>")

	return c.HTML(http.StatusOK, htmlBuilder.String())
}

func formatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

// Helper function to get file info (especially for symlinks)
func getFileInfo(path string, entry fs.DirEntry) (fs.FileInfo, error) {
	info, err := entry.Info()
	if err != nil {
		// If entry.Info() fails (e.g. broken symlink), try lstat
		info, err = os.Lstat(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, err
		}
	}
	return info, nil
}
