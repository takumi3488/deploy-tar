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
	// This MkdirAll is for the initial setup. If it's a PUT request,
	// the directory will be removed and recreated later.
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

	// Get request method - This block handles directory cleanup for PUT and is applicable to all file types.
	// It should be executed before deciding how to handle the file (extract or copy).
	method := c.Request().Method
	if method == http.MethodPut {
		c.Logger().Infof("PUT request detected for path: %s. Removing existing directory.", baseDirPath)
		// If PUT, remove existing directory
		if err := os.RemoveAll(baseDirPath); err != nil {
			c.Logger().Errorf("Failed to remove existing directory %s for PUT: %v", baseDirPath, err)
			c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to remove existing directory for PUT: "+err.Error()))
			return nil
		}
		// Recreate the directory
		c.Logger().Infof("Recreating directory %s for PUT.", baseDirPath)
		if err := os.MkdirAll(baseDirPath, 0755); err != nil {
			c.Logger().Errorf("Failed to recreate base directory %s for PUT: %v", baseDirPath, err)
			c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to recreate base directory for PUT: "+err.Error()))
			return nil
		}
	}

	// Determine file type and process accordingly
	isTar := strings.HasSuffix(fileHeader.Filename, ".tar")
	isGz := strings.HasSuffix(fileHeader.Filename, ".gz")
	isTgz := strings.HasSuffix(fileHeader.Filename, ".tgz")

	if !isTar && !isGz && !isTgz { // Not a .tar, .gz, or .tgz file, so copy directly
		targetPath := filepath.Join(baseDirPath, fileHeader.Filename)

		// Security check: Ensure the cleaned path is still within the baseDirPath
		cleanTarget := filepath.Clean(targetPath)
		cleanedBaseDirPath := filepath.Clean(baseDirPath) // Use cleaned baseDirPath for comparison

		if !strings.HasPrefix(cleanTarget, cleanedBaseDirPath+string(os.PathSeparator)) && cleanTarget != cleanedBaseDirPath {
			c.Logger().Warnf("Path traversal attempt detected for non-archive: %s (cleaned: %s)", fileHeader.Filename, cleanTarget)
			c.Error(echo.NewHTTPError(http.StatusBadRequest, "Invalid path for file (path traversal attempt)"))
			return nil
		}

		// Ensure parent directory exists for the file itself
		// For non-archive files, the baseDirPath might be the direct parent.
		// If fileHeader.Filename contains subdirectories, MkdirAll handles it.
		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0755); err != nil {
			c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create parent directory for file: "+err.Error()))
			return nil
		}

		outFile, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		if err != nil {
			c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create file: "+err.Error()))
			return nil
		}

		// Using a func to ensure outFile.Close() is called after copying
		copyErr := func() error {
			defer func() {
				if err := outFile.Close(); err != nil {
					c.Logger().Errorf("Failed to close output file %s: %v", cleanTarget, err)
				}
			}()
			// Reset src to the beginning for direct copy if it was read by gzip.NewReader before
			// This is important if src was already used by a gzip.NewReader attempt that failed or was not used.
			// However, in the current logic, if it's not tar/gz/tgz, src hasn't been read by gzip.NewReader yet.
			// For safety, if src could have been partially read, a Seek is needed.
			// Assuming fileHeader.Open() gives a fresh reader or one that can be re-read if necessary.
			// If src is an io.ReadSeeker, we could do: src.Seek(0, io.SeekStart)
			// For multipart.File, it is an io.ReadSeeker.
			if seeker, ok := src.(io.ReadSeeker); ok {
				if _, err := seeker.Seek(0, io.SeekStart); err != nil {
					c.Logger().Errorf("Failed to seek source file for %s: %v", cleanTarget, err)
					return echo.NewHTTPError(http.StatusInternalServerError, "Failed to seek source file: "+err.Error())
				}
			}

			if _, err := io.Copy(outFile, src); err != nil {
				c.Logger().Errorf("Failed to copy file content for %s: %v", cleanTarget, err)
				return echo.NewHTTPError(http.StatusInternalServerError, "Failed to copy file content: "+err.Error())
			}
			return nil
		}()
		if copyErr != nil {
			c.Error(copyErr)
			return nil
		}
		c.Logger().Infof("File %s uploaded successfully to %s", fileHeader.Filename, cleanTarget)
		return c.String(http.StatusOK, "File uploaded successfully")

	} else { // It's an archive file (.tar, .gz, or .tgz)
		var fileReader io.Reader = src // Start with the raw source

		// Check if the file is gzipped (only if it's .gz or .tgz)
		// .tar.gz and .tgz are handled here. .tar files go directly to tar.NewReader.
		if isGz || isTgz {
			// If src was already used by a previous NewReader (e.g. if we tried to auto-detect),
			// we might need to reset it. Here, we assume src is fresh or seekable.
			if seeker, ok := src.(io.ReadSeeker); ok {
				if _, err := seeker.Seek(0, io.SeekStart); err != nil {
					c.Logger().Errorf("Failed to seek source file for gzip: %v", err)
					return echo.NewHTTPError(http.StatusInternalServerError, "Failed to seek source file for gzip: "+err.Error())
				}
			}
			gzr, err := gzip.NewReader(src) // Pass (potentially reset) src to gzip reader
			if err != nil {
				c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create gzip reader: "+err.Error()))
				return nil
			}
			defer func() {
				if err := gzr.Close(); err != nil {
					c.Logger().Errorf("Failed to close gzip reader: %v", err)
				}
			}()
			fileReader = gzr // Update fileReader to be the gzip reader
		}
		// Now, fileReader is either src (for .tar only) or gzr (for .gz, .tgz)
		tarReader := tar.NewReader(fileReader)

		for {
			header, err := tarReader.Next()
			if err == io.EOF {
				break // End of tar archive
			}
			if err != nil {
				// Check if the error is due to reading a non-tar file that was gzipped (e.g. a single .gz file not .tar.gz)
				// tar.NewReader would return an error quickly.
				if strings.Contains(err.Error(), "tar: invalid tar header") && (isGz || isTgz) && !isTar {
					c.Logger().Warnf("File %s appears to be a GZip file but not a TAR archive. It was not extracted as TAR.", fileHeader.Filename)
					// If we wanted to save the decompressed .gz content, we'd need different logic here.
					// For now, we treat it as a tar extraction failure.
					c.Error(echo.NewHTTPError(http.StatusBadRequest, "File is GZipped but not a valid TAR archive: "+err.Error()))
					return nil
				}
				c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to read tar header: "+err.Error()))
				return nil
			}

			// Construct the target path for the entry
			target := filepath.Join(baseDirPath, header.Name)

			// Security check: Ensure the cleaned path is still within the baseDirPath
			cleanTarget := filepath.Clean(target)
			cleanedBaseDirPath := filepath.Clean(baseDirPath)
			if !strings.HasPrefix(cleanTarget, cleanedBaseDirPath+string(os.PathSeparator)) && cleanTarget != cleanedBaseDirPath {
				c.Logger().Warnf("Path traversal attempt detected: %s (cleaned: %s)", header.Name, cleanTarget)
				c.Error(echo.NewHTTPError(http.StatusBadRequest, "Invalid path in tar file (path traversal attempt)"))
				return nil
			}

			switch header.Typeflag {
			case tar.TypeDir:
				if err := os.MkdirAll(cleanTarget, os.FileMode(header.Mode)); err != nil { // Use cleanTarget
					c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create directory from tar: "+err.Error()))
					return nil
				}
			case tar.TypeReg:
				// Ensure parent directory exists for the file being extracted from tar
				if err := os.MkdirAll(filepath.Dir(cleanTarget), 0755); err != nil { // Use cleanTarget
					c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create parent directory for file: "+err.Error()))
					return nil
				}

				outFile, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode)) // Use cleanTarget
				if err != nil {
					c.Error(echo.NewHTTPError(http.StatusInternalServerError, "Failed to create file from tar: "+err.Error()))
					return nil
				}
				copyErr := func() error {
					defer func() {
						if err := outFile.Close(); err != nil {
							c.Logger().Errorf("Failed to close output file %s: %v", cleanTarget, err)
						}
					}()
					if _, err := io.Copy(outFile, tarReader); err != nil {
						c.Logger().Errorf("Failed to copy file content from tar for %s: %v", cleanTarget, err)
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
		c.Logger().Infof("Archive %s extracted successfully to %s", fileHeader.Filename, baseDirPath)
		return c.String(http.StatusOK, "Tar file extracted successfully")
	}
}
