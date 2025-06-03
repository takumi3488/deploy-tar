package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "deploytar/proto/fileservice"
)

// GRPCListDirectoryServer implements the FileService gRPC server
type GRPCListDirectoryServer struct {
	pb.UnimplementedFileServiceServer
}

// NewGRPCListDirectoryServer creates a new gRPC server instance
func NewGRPCListDirectoryServer() *GRPCListDirectoryServer {
	return &GRPCListDirectoryServer{}
}

// ListDirectory implements the ListDirectory RPC method
func (s *GRPCListDirectoryServer) ListDirectory(ctx context.Context, req *pb.ListDirectoryRequest) (*pb.ListDirectoryResponse, error) {
	pathPrefixEnv := os.Getenv("PATH_PREFIX")

	// Get directory from protobuf pointer
	var rawQuerySubDir string
	if req.Directory != nil {
		rawQuerySubDir = *req.Directory
	}

	// 1. Determine cleanedPathPrefix (for path validation and logic branching)
	var cleanedPathPrefix string
	if pathPrefixEnv != "" {
		cleanedPathPrefix = filepath.Clean(pathPrefixEnv)
		if cleanedPathPrefix == "." || cleanedPathPrefix == "/" {
			cleanedPathPrefix = ""
		}
	}

	// 2. Determine effectiveQuerySubDir (for file system access)
	effectiveQuerySubDir := rawQuerySubDir
	if cleanedPathPrefix != "" && rawQuerySubDir == "/" {
		effectiveQuerySubDir = ""
	}

	// PRELIMINARY TRAVERSAL CHECK
	if cleanedPathPrefix != "" {
		cleanedUserRequestPath := filepath.Clean(rawQuerySubDir)

		if strings.HasPrefix(cleanedUserRequestPath, "..") {
			return nil, status.Error(codes.PermissionDenied, "Access to the requested path is forbidden (path traversal attempt?)")
		}

		if filepath.IsAbs(cleanedUserRequestPath) && cleanedUserRequestPath != "/" {
			if !strings.HasPrefix(cleanedUserRequestPath, cleanedPathPrefix) {
				return nil, status.Error(codes.PermissionDenied, "Access to the requested path is forbidden")
			}
		}
	}

	// 3. Calculate targetDir (for file system access)
	targetFsPath := filepath.Clean(effectiveQuerySubDir)
	if targetFsPath == "" || targetFsPath == "." || targetFsPath == "/" {
		targetFsPath = "."
	}

	var baseDirForAccess string
	if cleanedPathPrefix != "" {
		prefixInfo, err := os.Stat(cleanedPathPrefix)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, status.Error(codes.NotFound, "Base directory not found")
			}
			return nil, status.Error(codes.Internal, "Failed to access base directory")
		}
		if !prefixInfo.IsDir() {
			return nil, status.Error(codes.InvalidArgument, "Base path is not a directory")
		}
		baseDirForAccess = cleanedPathPrefix
	} else {
		baseDirForAccess = "."
	}

	targetDir := filepath.Join(baseDirForAccess, targetFsPath)
	targetDir = filepath.Clean(targetDir)

	// 4. Path validation
	absTargetDir, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, status.Error(codes.Internal, "Internal server error during path resolution")
	}

	if cleanedPathPrefix != "" {
		absCleanedPathPrefix, err := filepath.Abs(cleanedPathPrefix)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to resolve prefix path")
		}

		relPath, err := filepath.Rel(absCleanedPathPrefix, absTargetDir)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to compute relative path")
		}

		if strings.HasPrefix(relPath, "..") || relPath == ".." {
			return nil, status.Error(codes.PermissionDenied, "Access to the requested path is forbidden (path traversal attempt?)")
		}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to get current working directory")
		}
		absCwd, err := filepath.Abs(cwd)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to resolve current working directory path")
		}

		relPath, err := filepath.Rel(absCwd, absTargetDir)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to compute relative path from cwd")
		}
		if strings.HasPrefix(relPath, "..") || relPath == ".." {
			return nil, status.Error(codes.PermissionDenied, "Access to the requested path is forbidden (path traversal attempt?)")
		}
	}

	// 5. Calculate requestedPathForDisplay
	requestedPathForDisplay := rawQuerySubDir
	if cleanedPathPrefix != "" && rawQuerySubDir == "/" {
		requestedPathForDisplay = ""
	}

	if requestedPathForDisplay == "" || requestedPathForDisplay == "." {
		requestedPathForDisplay = "/"
	} else {
		cleanedDisplayPath := filepath.Clean(requestedPathForDisplay)
		if cleanedDisplayPath != "." {
			requestedPathForDisplay = cleanedDisplayPath
		} else {
			requestedPathForDisplay = "/"
		}
	}

	// Check directory existence and read permission
	dirEntries, err := os.ReadDir(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			displayErrorPath := rawQuerySubDir
			if displayErrorPath == "" {
				displayErrorPath = "/"
			}
			return nil, status.Error(codes.NotFound, fmt.Sprintf("Directory not found: %s", displayErrorPath))
		}
		if os.IsPermission(err) {
			return nil, status.Error(codes.PermissionDenied, "Access to the requested path is forbidden")
		}
		return nil, status.Error(codes.Internal, "Failed to read directory")
	}

	// Prepare response
	var entries []*pb.DirectoryEntry
	var parentLink string

	// Link to parent directory (if not root)
	currentQueryDir := rawQuerySubDir
	if currentQueryDir != "" && currentQueryDir != "." {
		parentDir := filepath.Dir(strings.TrimSuffix(currentQueryDir, "/"))
		if parentDir == "." || parentDir == "/" {
			parentLink = ""
		} else {
			parentLink = parentDir
		}
	}

	for _, entry := range dirEntries {
		info, err := getFileInfo(filepath.Join(targetDir, entry.Name()), entry)
		if err != nil {
			// Skip entries we can't get info for
			continue
		}

		var entryType string
		var size string
		var link string

		if info.IsDir() {
			entryType = "directory"
		} else {
			entryType = "file"
			size = formatFileSize(info.Size())
		}

		// Generate link for gRPC (directory path for next request)
		subDir := rawQuerySubDir
		if subDir == "" || subDir == "." {
			link = entry.Name()
		} else {
			link = filepath.Join(subDir, entry.Name())
		}

		entryName := entry.Name()
		entries = append(entries, &pb.DirectoryEntry{
			Name: &entryName,
			Type: &entryType,
			Size: &size,
			Link: &link,
		})
	}

	response := &pb.ListDirectoryResponse{
		Path:       &requestedPathForDisplay,
		Entries:    entries,
		ParentLink: &parentLink,
	}

	return response, nil
}
