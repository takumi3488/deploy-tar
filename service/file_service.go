package service

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type DirectoryEntryService struct {
	Name string
	Type string
	Size string
	Link string
}

func formatFileSizeService(size int64) string {
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

func getFileInfoService(path string, entry fs.DirEntry) (fs.FileInfo, error) {
	info, err := entry.Info()
	if err != nil {
		return nil, err
	}

	if entry.Type()&fs.ModeSymlink != 0 {
		targetInfo, statErr := os.Stat(filepath.Join(path, entry.Name()))
		if statErr != nil {
			return nil, statErr
		}
		return targetInfo, nil
	}
	return info, nil
}

func ListDirectory(validatedAbsPath string, originalRequestPath string) ([]DirectoryEntryService, string, error) {
	dirEntries, err := os.ReadDir(validatedAbsPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read directory %s: %w", validatedAbsPath, err)
	}

	var entries []DirectoryEntryService
	var parentLink string

	cleanedOriginalRequestPath := filepath.Clean(originalRequestPath)
	if cleanedOriginalRequestPath == "." {
		cleanedOriginalRequestPath = "/"
	}
	if len(cleanedOriginalRequestPath) > 1 && strings.HasSuffix(cleanedOriginalRequestPath, string(filepath.Separator)) {
		cleanedOriginalRequestPath = strings.TrimSuffix(cleanedOriginalRequestPath, string(filepath.Separator))
	}

	if cleanedOriginalRequestPath != "" && cleanedOriginalRequestPath != "/" {
		parentDir := filepath.Dir(cleanedOriginalRequestPath)
		if parentDir == "." {
			parentLink = "/"
		} else {
			parentLink = parentDir
		}
	} else {
		parentLink = ""
	}

	for _, entry := range dirEntries {
		info, err := getFileInfoService(validatedAbsPath, entry)
		if err != nil {
			continue
		}

		var entryType string
		var size string
		var linkPath string

		if info.IsDir() {
			entryType = "directory"
		} else {
			entryType = "file"
			size = formatFileSizeService(info.Size())
		}

		currentLinkDir := cleanedOriginalRequestPath
		if currentLinkDir == "/" {
			currentLinkDir = ""
		}
		linkPath = filepath.Join(currentLinkDir, entry.Name())

		if !strings.HasPrefix(linkPath, "/") {
			linkPath = "/" + linkPath
		}

		entries = append(entries, DirectoryEntryService{
			Name: entry.Name(),
			Type: entryType,
			Size: size,
			Link: linkPath,
		})
	}
	return entries, parentLink, nil
}

func UploadFile(inputStream io.Reader, targetDirUserPath, fileName, pathPrefixEnv string, isPutRequest bool) (finalPath string, err error) {
	cleanedTargetUserPath := filepath.Clean(targetDirUserPath)

	var absValidatedTargetDir string
	cleanedPathPrefix := ""
	if pathPrefixEnv != "" {
		cleanedPathPrefix = filepath.Clean(pathPrefixEnv)
		if cleanedPathPrefix == "." || cleanedPathPrefix == "/" {
			cleanedPathPrefix = ""
		}
	}

	if cleanedTargetUserPath == "" && cleanedPathPrefix == "" {
		return "", fmt.Errorf("target directory cannot be empty")
	}
	if cleanedTargetUserPath == "." && cleanedPathPrefix == "" {
		return "", fmt.Errorf("target directory cannot be current directory shorthand without a prefix")
	}

	if strings.HasPrefix(cleanedTargetUserPath, string(os.PathSeparator)+"..") || strings.HasPrefix(cleanedTargetUserPath, ".."+string(os.PathSeparator)) || cleanedTargetUserPath == ".." {
		return "", fmt.Errorf("target directory cannot be a path traversal attempt: %s", targetDirUserPath)
	}

	if cleanedPathPrefix != "" {
		absCleanedPathPrefix, pathErr := filepath.Abs(cleanedPathPrefix)
		if pathErr != nil {
			return "", fmt.Errorf("failed to get absolute path for prefix '%s': %w", cleanedPathPrefix, pathErr)
		}

		if filepath.IsAbs(cleanedTargetUserPath) {
			absCleanedTargetUserPath, targetPathErr := filepath.Abs(cleanedTargetUserPath)
			if targetPathErr != nil {
				return "", fmt.Errorf("failed to get absolute path for target '%s': %w", cleanedTargetUserPath, targetPathErr)
			}
			if !strings.HasPrefix(absCleanedTargetUserPath, absCleanedPathPrefix) {
				return "", fmt.Errorf("absolute target directory '%s' is outside the scope of path prefix '%s'", targetDirUserPath, cleanedPathPrefix)
			}
			absValidatedTargetDir = absCleanedTargetUserPath
		} else {
			// 空文字列や"."の場合はプレフィックス直下を指す
			if cleanedTargetUserPath == "" || cleanedTargetUserPath == "." {
				absValidatedTargetDir = absCleanedPathPrefix
			} else {
				absValidatedTargetDir = filepath.Join(absCleanedPathPrefix, cleanedTargetUserPath)
			}
		}
	} else {
		var absErr error
		absValidatedTargetDir, absErr = filepath.Abs(cleanedTargetUserPath)
		if absErr != nil {
			return "", fmt.Errorf("failed to get absolute path for target: %w", absErr)
		}
	}
	absValidatedTargetDir = filepath.Clean(absValidatedTargetDir)

	if cleanedPathPrefix != "" {
		prefixInfo, statErr := os.Stat(cleanedPathPrefix)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return "", fmt.Errorf("path prefix directory '%s' does not exist", cleanedPathPrefix)
			}
			return "", fmt.Errorf("failed to stat path prefix directory '%s': %w", cleanedPathPrefix, statErr)
		}
		if !prefixInfo.IsDir() {
			return "", fmt.Errorf("path prefix '%s' is not a directory", cleanedPathPrefix)
		}
	}

	var effectiveBaseDir string
	if cleanedPathPrefix != "" {
		effectiveBaseDir, _ = filepath.Abs(cleanedPathPrefix)
	} else {
		if filepath.IsAbs(targetDirUserPath) {
			effectiveBaseDir = absValidatedTargetDir
		} else {
			effectiveBaseDir, _ = os.Getwd()
		}
	}

	relPath, relErr := filepath.Rel(effectiveBaseDir, absValidatedTargetDir)
	if relErr != nil {
		return "", fmt.Errorf("internal error validating path relationship: %w", relErr)
	}
	if strings.HasPrefix(relPath, "..") || relPath == ".." {
		return "", fmt.Errorf("target path '%s' attempts to traverse outside its allowed scope", targetDirUserPath)
	}

	if isPutRequest {
		if err := os.RemoveAll(absValidatedTargetDir); err != nil {
			if !os.IsNotExist(err) {
				return "", fmt.Errorf("failed to remove existing directory '%s' for PUT: %w", absValidatedTargetDir, err)
			}
		}
	}
	if err := os.MkdirAll(absValidatedTargetDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create target directory '%s': %w", absValidatedTargetDir, err)
	}

	fileNameLower := strings.ToLower(fileName)
	isTgz := strings.HasSuffix(fileNameLower, ".tgz")
	isTarGz := strings.HasSuffix(fileNameLower, ".tar.gz")
	isTar := strings.HasSuffix(fileNameLower, ".tar") && !isTarGz
	isGz := strings.HasSuffix(fileNameLower, ".gz") && !isTarGz && !isTgz

	if isTgz || isTarGz {
		gzr, errGzip := gzip.NewReader(inputStream)
		if errGzip != nil {
			return "", fmt.Errorf("failed to create gzip reader for archive '%s': %w", fileName, errGzip)
		}
		defer func() {
			if err := gzr.Close(); err != nil {
				_ = err
			}
		}()
		if errExtract := extractTar(gzr, absValidatedTargetDir, fileName); errExtract != nil {
			return "", errExtract
		}
		finalPath = absValidatedTargetDir
	} else if isTar {
		if errExtract := extractTar(inputStream, absValidatedTargetDir, fileName); errExtract != nil {
			return "", errExtract
		}
		finalPath = absValidatedTargetDir
	} else if isGz {
		gzr, errGzip := gzip.NewReader(inputStream)
		if errGzip != nil {
			return "", fmt.Errorf("failed to create gzip reader for '%s': %w", fileName, errGzip)
		}
		defer func() {
			if err := gzr.Close(); err != nil {
				_ = err
			}
		}()

		targetFileName := strings.TrimSuffix(fileName, ".gz")
		if targetFileName == "" {
			targetFileName = "gzipped_file"
		}
		absFinalFilePath := filepath.Join(absValidatedTargetDir, filepath.Clean(targetFileName))
		if !strings.HasPrefix(absFinalFilePath, absValidatedTargetDir+string(os.PathSeparator)) && absFinalFilePath != absValidatedTargetDir {
			return "", fmt.Errorf("path traversal attempt for gzipped file target '%s'", targetFileName)
		}
		if errMkdir := os.MkdirAll(filepath.Dir(absFinalFilePath), 0755); errMkdir != nil {
			return "", fmt.Errorf("failed to create parent directory for gzipped file '%s': %w", absFinalFilePath, errMkdir)
		}

		outFile, errOpen := os.OpenFile(absFinalFilePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		if errOpen != nil {
			return "", fmt.Errorf("failed to create file for gzipped content '%s': %w", absFinalFilePath, errOpen)
		}
		_, copyErr := io.Copy(outFile, gzr)
		if closeErr := outFile.Close(); closeErr != nil && copyErr == nil {
			return "", fmt.Errorf("failed to close output file for gzipped content '%s': %w", absFinalFilePath, closeErr)
		}
		if copyErr != nil {
			if err := os.Remove(absFinalFilePath); err != nil {
				_ = err
			}
			return "", fmt.Errorf("failed to copy gzipped file content to '%s': %w", absFinalFilePath, copyErr)
		}
		finalPath = absFinalFilePath
	} else {
		cleanedFileName := filepath.Clean(fileName)
		if strings.HasPrefix(cleanedFileName, string(os.PathSeparator)) || strings.HasPrefix(cleanedFileName, "..") {
			return "", fmt.Errorf("invalid characters or traversal attempt in filename '%s'", fileName)
		}
		absFinalFilePath := filepath.Join(absValidatedTargetDir, cleanedFileName)

		if !strings.HasPrefix(absFinalFilePath, absValidatedTargetDir+string(os.PathSeparator)) && absFinalFilePath != absValidatedTargetDir {
			return "", fmt.Errorf("path traversal attempt for file target '%s'", fileName)
		}
		if errMkdir := os.MkdirAll(filepath.Dir(absFinalFilePath), 0755); errMkdir != nil {
			return "", fmt.Errorf("failed to create parent directory for file '%s': %w", absFinalFilePath, errMkdir)
		}

		outFile, errOpen := os.OpenFile(absFinalFilePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		if errOpen != nil {
			return "", fmt.Errorf("failed to create file '%s': %w", absFinalFilePath, errOpen)
		}
		_, copyErr := io.Copy(outFile, inputStream)
		if closeErr := outFile.Close(); closeErr != nil && copyErr == nil {
			return "", fmt.Errorf("failed to close output file '%s': %w", absFinalFilePath, closeErr)
		}
		if copyErr != nil {
			if err := os.Remove(absFinalFilePath); err != nil {
				_ = err
			}
			return "", fmt.Errorf("failed to copy file content to '%s': %w", absFinalFilePath, copyErr)
		}
		finalPath = absFinalFilePath
	}

	return finalPath, nil
}

func extractTar(r io.Reader, baseExtractDir string, archiveName string) error {
	tr := tar.NewReader(r)
	headerProcessedSuccessfullyAtLeastOnce := false

	for {
		header, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if !headerProcessedSuccessfullyAtLeastOnce && archiveName != "" {
					return fmt.Errorf("empty or invalid tar archive '%s': no headers found", archiveName)
				}
				break
			}
			return fmt.Errorf("failed to read tar header from archive '%s': %w", archiveName, err)
		}
		headerProcessedSuccessfullyAtLeastOnce = true

		cleanedHeaderName := filepath.Clean(header.Name)
		if filepath.IsAbs(cleanedHeaderName) || strings.HasPrefix(cleanedHeaderName, ".."+string(os.PathSeparator)) || cleanedHeaderName == ".." {
			return fmt.Errorf("tar archive '%s' contains potentially unsafe path entry '%s'", archiveName, header.Name)
		}

		targetItemPath := filepath.Join(baseExtractDir, cleanedHeaderName)
		if !strings.HasPrefix(targetItemPath, baseExtractDir+string(os.PathSeparator)) && targetItemPath != baseExtractDir {
			return fmt.Errorf("path traversal attempt in archive '%s': entry '%s' resolves to '%s' which is outside extraction directory '%s'", archiveName, header.Name, targetItemPath, baseExtractDir)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetItemPath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory '%s' from archive '%s': %w", targetItemPath, archiveName, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetItemPath), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory for file '%s' from archive '%s': %w", targetItemPath, archiveName, err)
			}
			itemOutFile, errOpen := os.OpenFile(targetItemPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
			if errOpen != nil {
				return fmt.Errorf("failed to create file '%s' from archive '%s': %w", targetItemPath, archiveName, errOpen)
			}
			var itemCopyErr error
			if header.Size > 0 {
				_, itemCopyErr = io.Copy(itemOutFile, tr)
			}
			closeErr := itemOutFile.Close()

			if itemCopyErr != nil {
				if err := os.Remove(targetItemPath); err != nil {
					_ = err
				}
				return fmt.Errorf("failed to copy content to '%s' from archive '%s': %w", targetItemPath, archiveName, itemCopyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("failed to close file '%s' from archive '%s': %w", targetItemPath, archiveName, closeErr)
			}
		default:
		}
	}
	return nil
}
