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
	// Get PATH_PREFIX environment variable
	pathPrefix := os.Getenv("PATH_PREFIX")

	// Get the destination directory
	baseDirPath := c.FormValue("path")
	if baseDirPath == "" {
		c.Error(echo.NewHTTPError(http.StatusBadRequest, "Destination directory not specified"))
		return nil
	}

	// Validate path against PATH_PREFIX if it's set
	if pathPrefix != "" {
		// Clean both paths first.
		// This handles redundant separators, ".", ".." etc.
		// Normalize both paths using filepath.Clean.
		// This handles ., .., and redundant slashes.
		cleanedBasePath := filepath.Clean(baseDirPath)
		cleanedPathPrefix := filepath.Clean(pathPrefix)

		// Split paths into components. Handle leading/trailing slashes by cleaning first.
		// filepath.ToSlash ensures consistent separators for splitting.
		basePathComponents := strings.Split(filepath.ToSlash(cleanedBasePath), "/")
		pathPrefixComponents := strings.Split(filepath.ToSlash(cleanedPathPrefix), "/")

		// Remove empty strings that can result from leading/trailing slashes after split
		// For example, "/a/b" -> ["", "a", "b"]. We care about actual path segments.
		filterEmpty := func(s []string) []string {
			var r []string
			for _, str := range s {
				if str != "" {
					r = append(r, str)
				}
			}
			return r
		}
		basePathComponents = filterEmpty(basePathComponents)
		pathPrefixComponents = filterEmpty(pathPrefixComponents)
		
		allowed := false
		if len(pathPrefixComponents) == 0 { // Empty prefix might mean allow all or disallow all based on policy. Assume allow for now if prefix is effectively empty.
			allowed = true
		} else if len(basePathComponents) >= len(pathPrefixComponents) {
			// Check if pathPrefixComponents is a subsequence of basePathComponents
			// This means base path contains the prefix path structure.
			// Example: base=["tmp", "foo", "bar", "data"], prefix=["foo", "bar"] -> true
			for i := 0; i <= len(basePathComponents)-len(pathPrefixComponents); i++ {
				match := true
				for j := 0; j < len(pathPrefixComponents); j++ {
					if basePathComponents[i+j] != pathPrefixComponents[j] {
						match = false
						break
					}
				}
				if match {
					allowed = true
					break
				}
			}
		}


		if !allowed {
			c.Logger().Infof(
				"Path validation failed. BasePath: '%s' (components: %v), PathPrefix: '%s' (components: %v). Allowed: %t",
				baseDirPath, basePathComponents,
				pathPrefix, pathPrefixComponents,
				allowed,
			)
			c.Error(echo.NewHTTPError(http.StatusBadRequest, "Path is not allowed"))
			return nil
		}
	}

	// Ensure the base directory exists
	if err := os.MkdirAll(baseDirPath, 0755); err != nil {
		c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create base directory: "+err.Error()))
		return nil
	}

	// Get the tar file
	fileHeader, err := c.FormFile("tarfile")
	if err != nil {
		c.Error(echo.NewHTTPError(http.StatusBadRequest, "Tar file not found in request: "+err.Error()))
		return nil
	}

	src, err := fileHeader.Open()
	if err != nil {
		c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to open uploaded tar file: "+err.Error()))
		return nil
	}
	defer func() {
		if err := src.Close(); err != nil {
			c.Logger().Errorf("Failed to close source file: %v", err)
		}
	}()

	var fileReader io.Reader = src

	// Check if the file is gzipped
	if strings.HasSuffix(fileHeader.Filename, ".gz") || strings.HasSuffix(fileHeader.Filename, ".tgz") {
		gzr, err := gzip.NewReader(src)
		if err != nil {
			c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create gzip reader: "+err.Error()))
			return nil
		}
		defer func() {
			if err := gzr.Close(); err != nil {
				c.Logger().Errorf("Failed to close gzip reader: %v", err)
			}
		}()
		fileReader = gzr
	}

	tarReader := tar.NewReader(fileReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // End of tar archive
		}
		if err != nil {
			c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to read tar header: "+err.Error()))
			return nil
		}

		// Construct the target path for the entry
		// IMPORTANT: Sanitize the header.Name to prevent path traversal attacks
		target := filepath.Join(baseDirPath, header.Name)

		// Security check: Ensure the cleaned path is still within the baseDirPath
		cleanTarget := filepath.Clean(target)
		if !strings.HasPrefix(cleanTarget, filepath.Clean(baseDirPath)+string(os.PathSeparator)) && cleanTarget != filepath.Clean(baseDirPath) {
			c.Logger().Warnf("Path traversal attempt detected: %s (cleaned: %s)", header.Name, cleanTarget)
			c.Error(echo.NewHTTPError(http.StatusBadRequest, "Invalid path in tar file (path traversal attempt)"))
			return nil
		}

		switch header.Typeflag {
		case tar.TypeDir:
			// Create directory
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create directory from tar: "+err.Error()))
				return nil
			}
		case tar.TypeReg:
			// Create file
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create parent directory for file: "+err.Error()))
				return nil
			}

			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create file from tar: "+err.Error()))
				return nil
			}
			// Using a func to ensure outFile.Close() is called after copying
			copyErr := func() error {
				defer func() {
					if err := outFile.Close(); err != nil {
						c.Logger().Errorf("Failed to close output file %s: %v", target, err)
					}
				}()
				if _, err := io.Copy(outFile, tarReader); err != nil {
					c.Logger().Errorf("Failed to copy file content from tar for %s: %v", target, err)
					return echo.NewHTTPError(http.StatusInternalServerError, "Failed to copy file content from tar: "+err.Error())
				}
				return nil
			}()
			if copyErr != nil {
				c.Error(copyErr)
				return nil
			}

		default:
			c.Logger().Infof("Unsupported tar entry type %c for %s", header.Typeflag, header.Name)
		}
	}

	return c.String(http.StatusOK, "Tar file extracted successfully")
}
