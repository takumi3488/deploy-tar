package handler

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "deploytar/proto/deploytar/proto/fileservice/v1"
)

// UploadFile implements the UploadFile RPC method for the GRPCListDirectoryServer.
// The GRPCListDirectoryServer type is defined in grpc_list.go.
func (s *GRPCListDirectoryServer) UploadFile(stream pb.FileService_UploadFileServer) error {
	// 1. Receive file information (path, filename) from the first message
	req, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return status.Errorf(codes.InvalidArgument, "No data received from client")
		}
		return status.Errorf(codes.Internal, "Failed to receive upload request: %v", err)
	}

	fileInfo := req.GetInfo()
	if fileInfo == nil {
		return status.Errorf(codes.InvalidArgument, "Missing file info in the first request. First message must contain file metadata.")
	}

	baseDirPath := fileInfo.GetPath()
	fileName := fileInfo.GetFilename()

	if baseDirPath == "" {
		return status.Errorf(codes.InvalidArgument, "Destination directory not specified")
	}
	if fileName == "" {
		return status.Errorf(codes.InvalidArgument, "Filename not specified")
	}

	// 2. Path validation (adapted from handler/upload.go)
	pathPrefixEnv := os.Getenv("PATH_PREFIX")
	// Clean baseDirPath once here for consistent use
	cleanedBaseDirPath := filepath.Clean(baseDirPath)

	if pathPrefixEnv != "" {
		cleanedPathPrefix := filepath.Clean(pathPrefixEnv)
		// Pass cleanedBaseDirPath and cleanedPathPrefix to isValidGrpcUploadPath
		if !isValidGrpcUploadPath(cleanedBaseDirPath, cleanedPathPrefix) {
			return status.Errorf(codes.PermissionDenied, "Path is not allowed: %s", baseDirPath)
		}
	}

	// 3. Handle PUT-like behavior: remove and recreate directory
	// Ensure the path to be removed/recreated is based on the cleaned path.
	if err := os.RemoveAll(cleanedBaseDirPath); err != nil {
		return status.Errorf(codes.Internal, "Failed to remove existing directory %s for upload: %v", cleanedBaseDirPath, err)
	}
	if err := os.MkdirAll(cleanedBaseDirPath, 0755); err != nil {
		return status.Errorf(codes.Internal, "Failed to recreate base directory %s for upload: %v", cleanedBaseDirPath, err)
	}

	// 4. Create a temporary file to store the uploaded content
	tempFile, err := os.CreateTemp("", "grpc-upload-*.tmp")
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to create temporary file: %v", err)
	}
	defer func() {
		if removeErr := os.Remove(tempFile.Name()); removeErr != nil {
			log.Printf("Failed to remove temporary file %s: %v", tempFile.Name(), removeErr)
		}
	}()
	// tempFile.Close() will be called before os.Remove due to defer order (LIFO)
	// or explicitly before operations that need it closed (like reopening for read).

	// 5. Receive file chunks and write to the temporary file
	for {
		chunkReq, err := stream.Recv()
		if err == io.EOF {
			// End of stream, all chunks received
			break
		}
		if err != nil {
			if closeErr := tempFile.Close(); closeErr != nil {
				log.Printf("Failed to close temporary file: %v", closeErr)
			}
			return status.Errorf(codes.Internal, "Failed to receive file chunk: %v", err)
		}

		// Check if the message is FileInfo again, which is invalid after the first one.
		if chunkReq.GetInfo() != nil {
			if closeErr := tempFile.Close(); closeErr != nil {
				log.Printf("Failed to close temporary file: %v", closeErr)
			}
			return status.Errorf(codes.InvalidArgument, "Received unexpected FileInfo message after the first one.")
		}

		chunkData := chunkReq.GetChunkData()
		if chunkData == nil {
			// This case should ideally not happen if client sends valid messages (either chunk or EOF).
			// If it's not EOF and not an error, but chunkData is nil, it's ambiguous.
			// Let's treat it as an issue or skip. For now, skip.
			continue
		}

		if _, err := tempFile.Write(chunkData); err != nil {
			if closeErr := tempFile.Close(); closeErr != nil {
				log.Printf("Failed to close temporary file: %v", closeErr)
			}
			return status.Errorf(codes.Internal, "Failed to write to temporary file: %v", err)
		}
	}

	// Ensure all data is written to disk and close the file for writing.
	if err := tempFile.Sync(); err != nil {
		if closeErr := tempFile.Close(); closeErr != nil {
			log.Printf("Failed to close temporary file: %v", closeErr)
		}
		return status.Errorf(codes.Internal, "Failed to sync temporary file: %v", err)
	}
	if err := tempFile.Close(); err != nil {
		return status.Errorf(codes.Internal, "Failed to close temporary file after writing: %v", err)
	}

	// Re-open the temporary file for reading
	readOnlyTempFile, err := os.Open(tempFile.Name())
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to re-open temporary file for reading: %v", err)
	}
	defer func() {
		if closeErr := readOnlyTempFile.Close(); closeErr != nil {
			log.Printf("Failed to close temporary file: %v", closeErr)
		}
	}()

	// 6. Process the uploaded file (tar extraction or direct copy)
	fileNameLower := strings.ToLower(fileName)
	isActualTgz := strings.HasSuffix(fileNameLower, ".tgz")
	isActualTarGz := strings.HasSuffix(fileNameLower, ".tar.gz")

	isPlainTar := strings.HasSuffix(fileNameLower, ".tar") && !isActualTarGz
	isPlainGz := strings.HasSuffix(fileNameLower, ".gz") && !isActualTarGz && !isActualTgz

	// Process based on determined file type
	if isActualTgz || isActualTarGz { // Handles .tgz and .tar.gz
		// These require gzip decompression followed by tar extraction.
		var gzippedStream io.Reader = readOnlyTempFile
		gzr, err := gzip.NewReader(gzippedStream)
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "Failed to create gzip reader for archive '%s': %v", fileName, err)
		}
		defer func() {
			if closeErr := gzr.Close(); closeErr != nil {
				log.Printf("Failed to close gzip reader: %v", closeErr)
			}
		}()

		tarReader := tar.NewReader(gzr)
		var headerProcessedSuccessfullyAtLeastOnce bool
		for {
			header, err := tarReader.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					// Only break on EOF if we've successfully processed at least one header
					// If we haven't processed any headers and get EOF, treat it as corrupt/invalid data
					if !headerProcessedSuccessfullyAtLeastOnce {
						return status.Errorf(codes.Internal, "Failed to read tar header from archive '%s': %v", fileName, err)
					}
					break
				}
				// All other tar header reading errors are treated as Internal
				return status.Errorf(codes.Internal, "Failed to read tar header from archive '%s': %v", fileName, err)
			}
			headerProcessedSuccessfullyAtLeastOnce = true

			targetItemPath := filepath.Join(cleanedBaseDirPath, header.Name)
			cleanTargetItemPath := filepath.Clean(targetItemPath)

			if !strings.HasPrefix(cleanTargetItemPath, cleanedBaseDirPath+string(os.PathSeparator)) && cleanTargetItemPath != cleanedBaseDirPath {
				return status.Errorf(codes.PermissionDenied, "Path traversal attempt in archive '%s': entry '%s'", fileName, header.Name)
			}

			switch header.Typeflag {
			case tar.TypeDir:
				if err := os.MkdirAll(cleanTargetItemPath, os.FileMode(header.Mode)); err != nil {
					return status.Errorf(codes.Internal, "Failed to create directory '%s' from archive '%s': %v", cleanTargetItemPath, fileName, err)
				}
			case tar.TypeReg:
				if err := os.MkdirAll(filepath.Dir(cleanTargetItemPath), 0755); err != nil {
					return status.Errorf(codes.Internal, "Failed to create parent directory for '%s' from archive '%s': %v", cleanTargetItemPath, fileName, err)
				}
				itemOutFile, err := os.OpenFile(cleanTargetItemPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
				if err != nil {
					return status.Errorf(codes.Internal, "Failed to create file '%s' from archive '%s': %v", cleanTargetItemPath, fileName, err)
				}
				var itemCopyErr error
				if header.Size > 0 { // Only copy if there's content
					_, itemCopyErr = io.Copy(itemOutFile, tarReader)
				}

				closeErr := itemOutFile.Close() // Close the file first

				if itemCopyErr != nil {
					if removeErr := os.Remove(cleanTargetItemPath); removeErr != nil {
						log.Printf("Failed to remove partially written file %s: %v", cleanTargetItemPath, removeErr)
					}
					return status.Errorf(codes.Internal, "Failed to copy content to '%s' from archive '%s': %v", cleanTargetItemPath, fileName, itemCopyErr)
				}
				if closeErr != nil {
					// If copy was OK but close failed, the file might still be problematic.
					// Depending on desired atomicity, one might also remove here.
					// For now, report close error if copy was fine.
					return status.Errorf(codes.Internal, "Failed to close file '%s' from archive '%s': %v", cleanTargetItemPath, fileName, closeErr)
				}
			}
		}
		// After the tar processing loop for .tgz or .tar.gz files.
		// Check if at least one header was processed successfully.
		// This handles cases where the archive is gzipped but contains no valid tar headers,
		// or is an empty tar archive within the gzip stream.
		if !headerProcessedSuccessfullyAtLeastOnce {
			return status.Errorf(codes.InvalidArgument, "Invalid or empty tar archive: no valid entries found in %s", fileName)
		}
	} else if isPlainTar { // Handles plain .tar files (not gzipped, not .tar.gz)
		tarReader := tar.NewReader(readOnlyTempFile) // Use the temp file directly
		var headerProcessedSuccessfullyAtLeastOnce bool
		for {
			header, err := tarReader.Next()
			if err != nil {
				if errors.Is(err, io.EOF) {
					if !headerProcessedSuccessfullyAtLeastOnce {
						return status.Errorf(codes.InvalidArgument, "Empty or invalid tar archive '%s': no headers found", fileName)
					}
					break
				}
				return status.Errorf(codes.Internal, "Failed to read tar header from '%s': %v", fileName, err)
			}
			headerProcessedSuccessfullyAtLeastOnce = true

			targetItemPath := filepath.Join(cleanedBaseDirPath, header.Name)
			cleanTargetItemPath := filepath.Clean(targetItemPath)

			if !strings.HasPrefix(cleanTargetItemPath, cleanedBaseDirPath+string(os.PathSeparator)) && cleanTargetItemPath != cleanedBaseDirPath {
				return status.Errorf(codes.PermissionDenied, "Path traversal attempt in tar archive '%s': entry '%s'", fileName, header.Name)
			}

			switch header.Typeflag {
			case tar.TypeDir:
				if err := os.MkdirAll(cleanTargetItemPath, os.FileMode(header.Mode)); err != nil {
					return status.Errorf(codes.Internal, "Failed to create directory '%s' from tar '%s': %v", cleanTargetItemPath, fileName, err)
				}
			case tar.TypeReg:
				if err := os.MkdirAll(filepath.Dir(cleanTargetItemPath), 0755); err != nil {
					return status.Errorf(codes.Internal, "Failed to create parent directory for '%s' from tar '%s': %v", cleanTargetItemPath, fileName, err)
				}
				itemOutFile, err := os.OpenFile(cleanTargetItemPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
				if err != nil {
					return status.Errorf(codes.Internal, "Failed to create file '%s' from tar '%s': %v", cleanTargetItemPath, fileName, err)
				}
				var itemCopyErr error
				if header.Size > 0 { // Only copy if there's content
					_, itemCopyErr = io.Copy(itemOutFile, tarReader)
				}

				closeErr := itemOutFile.Close() // Close the file first

				if itemCopyErr != nil {
					if removeErr := os.Remove(cleanTargetItemPath); removeErr != nil {
						log.Printf("Failed to remove partially written file %s: %v", cleanTargetItemPath, removeErr)
					}
					return status.Errorf(codes.Internal, "Failed to copy content to '%s' from tar '%s': %v", cleanTargetItemPath, fileName, itemCopyErr)
				}
				if closeErr != nil {
					return status.Errorf(codes.Internal, "Failed to close file '%s' from tar '%s': %v", cleanTargetItemPath, fileName, closeErr)
				}
			}
		}
	} else if isPlainGz { // Handles plain .gz files (not .tar.gz or .tgz)
		// Decompress and save as a single file.
		gzr, err := gzip.NewReader(readOnlyTempFile)
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "Failed to create gzip reader for '%s': %v", fileName, err)
		}
		defer func() {
			if closeErr := gzr.Close(); closeErr != nil {
				log.Printf("Failed to close gzip reader: %v", closeErr)
			}
		}()

		// Save with .gz suffix removed.
		targetFileName := strings.TrimSuffix(fileName, ".gz")
		if targetFileName == "" { // Handle cases like ".gz" if that's possible/problematic
			targetFileName = "gzipped_file" // Default name if suffix removal results in empty
		}
		targetPath := filepath.Join(cleanedBaseDirPath, targetFileName)
		cleanTargetPath := filepath.Clean(targetPath)

		if !strings.HasPrefix(cleanTargetPath, cleanedBaseDirPath+string(os.PathSeparator)) && cleanTargetPath != cleanedBaseDirPath {
			return status.Errorf(codes.PermissionDenied, "Path traversal attempt for gzipped file '%s' (target '%s')", fileName, targetFileName)
		}

		if err := os.MkdirAll(filepath.Dir(cleanTargetPath), 0755); err != nil {
			return status.Errorf(codes.Internal, "Failed to create parent directory for gzipped file '%s': %v", cleanTargetPath, err)
		}
		outFile, err := os.OpenFile(cleanTargetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		if err != nil {
			return status.Errorf(codes.Internal, "Failed to create file for gzipped content '%s': %v", cleanTargetPath, err)
		}

		_, copyErr := io.Copy(outFile, gzr)
		if closeErr := outFile.Close(); closeErr != nil && copyErr == nil {
			return status.Errorf(codes.Internal, "Failed to close output file for gzipped content '%s': %v", cleanTargetPath, closeErr)
		}
		if copyErr != nil {
			return status.Errorf(codes.Internal, "Failed to copy gzipped file content to '%s': %v", cleanTargetPath, copyErr)
		}
	} else { // Other file types (not .tar, .gz, .tgz, .tar.gz) - copy directly
		targetPath := filepath.Join(cleanedBaseDirPath, fileName)
		cleanTargetPath := filepath.Clean(targetPath)

		if !strings.HasPrefix(cleanTargetPath, cleanedBaseDirPath+string(os.PathSeparator)) && cleanTargetPath != cleanedBaseDirPath {
			return status.Errorf(codes.PermissionDenied, "Invalid path for file (path traversal attempt): %s", fileName)
		}

		if err := os.MkdirAll(filepath.Dir(cleanTargetPath), 0755); err != nil {
			return status.Errorf(codes.Internal, "Failed to create parent directory for file: %v", err)
		}

		outFile, err := os.OpenFile(cleanTargetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
		if err != nil {
			return status.Errorf(codes.Internal, "Failed to create file: %v", err)
		}

		_, copyErr := io.Copy(outFile, readOnlyTempFile)
		if closeErr := outFile.Close(); closeErr != nil && copyErr == nil {
			return status.Errorf(codes.Internal, "Failed to close output file %s: %v", cleanTargetPath, closeErr)
		}
		if copyErr != nil {
			return status.Errorf(codes.Internal, "Failed to copy file content: %v", copyErr)
		}
	}

	// 7. Send response
	// For archives, the "path" is the extraction directory. For single files, it's the file path.
	finalResponsePath := cleanedBaseDirPath
	// For archives (.tgz, .tar.gz, .tar), the "path" is the extraction directory (cleanedBaseDirPath).
	// For single files, it's the file path.
	// For single .gz files, it's the decompressed file path.
	if isActualTgz || isActualTarGz || isPlainTar {
		// finalResponsePath remains cleanedBaseDirPath for these archive types
	} else if isPlainGz { // Plain .gz (decompressed and saved)
		finalResponsePath = filepath.Join(cleanedBaseDirPath, strings.TrimSuffix(fileName, ".gz"))
	} else { // Other file types (direct copy)
		finalResponsePath = filepath.Join(cleanedBaseDirPath, fileName)
	}
	finalResponsePath = filepath.Clean(finalResponsePath)

	return stream.SendAndClose(&pb.UploadFileResponse{
		Message:  func() *string { s := "File uploaded successfully"; return &s }(),
		FilePath: &finalResponsePath,
	})
}

// isValidGrpcUploadPath is a helper function adapted from handler/upload.go's path validation logic.
// It checks if the target baseDirPath is allowed based on the pathPrefixInput.
// Parameters:
//
//	cleanedBaseDirPath: The user-provided path for upload, already cleaned by filepath.Clean.
//	cleanedPathPrefix: The PATH_PREFIX environment variable, already cleaned by filepath.Clean.
//
// Returns true if the path is allowed, false otherwise.
func isValidGrpcUploadPath(cleanedBaseDirPath string, cleanedPathPrefix string) bool {
	// If cleanedPathPrefix is empty (e.g., PATH_PREFIX was "" or "/"), all paths are allowed.
	if cleanedPathPrefix == "" || cleanedPathPrefix == "." || cleanedPathPrefix == "/" {
		return true
	}

	// Split paths into components.
	// filepath.ToSlash ensures consistent separators for splitting.
	basePathComponents := strings.Split(filepath.ToSlash(cleanedBaseDirPath), "/")
	pathPrefixComponents := strings.Split(filepath.ToSlash(cleanedPathPrefix), "/")

	// filterEmpty removes empty strings that can result from leading/trailing slashes.
	// This exact logic is from handler/upload.go
	filterEmpty := func(s []string) []string {
		var r []string
		for _, str := range s {
			if str != "" { // Also implicitly filters "." if path was cleaned to "." then split.
				r = append(r, str)
			}
		}
		return r
	}
	basePathComponents = filterEmpty(basePathComponents)
	pathPrefixComponents = filterEmpty(pathPrefixComponents)

	// If pathPrefixComponents is empty after filtering (e.g. prefix was effectively root), allow.
	if len(pathPrefixComponents) == 0 {
		return true
	}

	// Base path must be at least as long as the prefix path to contain it.
	if len(basePathComponents) < len(pathPrefixComponents) {
		return false
	}

	// Check if pathPrefixComponents is a subsequence (specifically, a prefix of a subsequence)
	// of basePathComponents. The original logic checks for any occurrence.
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
			return true
		}
	}
	return false
}
