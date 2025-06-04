package handler

import (
	"context"
	"deploytar/service" // Assuming 'deploytar' is the module name
	"errors"
	"fmt"
	"os"
	"strings"

	pb "deploytar/proto/deploytar/proto/fileservice/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	// "google.golang.org/protobuf/types/known/wrapperspb" // Not needed if using *string directly
)

// GRPCListDirectoryServer implements the gRPC server for listing directories.
type GRPCListDirectoryServer struct {
	pb.UnimplementedFileServiceServer // Embed for forward compatibility
}

// NewGRPCListDirectoryServer creates a new server instance.
func NewGRPCListDirectoryServer() *GRPCListDirectoryServer {
	return &GRPCListDirectoryServer{}
}

// ListDirectory is the gRPC handler for listing directory contents.
func (s *GRPCListDirectoryServer) ListDirectory(ctx context.Context, req *pb.ListDirectoryRequest) (*pb.ListDirectoryResponse, error) {
	pathPrefixEnv := os.Getenv("PATH_PREFIX")
	rawQuerySubDir := ""
	if req.Directory != nil { // Check if Directory field is set
		rawQuerySubDir = req.GetDirectory() // Use GetDirectory() to access the value of the pointer
	}

	validatedAbsPath, displayPathFromService, err := service.ResolveAndValidatePath(rawQuerySubDir, pathPrefixEnv)
	if err != nil {
		// Map service errors to gRPC status errors
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			return nil, status.Error(codes.NotFound, errMsg)
		}
		if strings.Contains(errMsg, "is not a directory") {
			return nil, status.Error(codes.InvalidArgument, errMsg)
		}
		if strings.Contains(errMsg, "forbidden") ||
			strings.Contains(errMsg, "traversal") ||
			strings.Contains(errMsg, "outside its allowed scope") ||
			strings.Contains(errMsg, "outside CWD") ||
			strings.Contains(errMsg, "outside prefix") {
			return nil, status.Error(codes.PermissionDenied, errMsg)
		}
		// Log internal errors if a logger is available in 's' or globally
		// log.Printf("Internal path validation error: %v", err)
		return nil, status.Error(codes.Internal, "Internal server error during path validation: "+errMsg)
	}

	// Call service.ListDirectory
	serviceEntries, serviceParentLink, err := service.ListDirectory(validatedAbsPath, rawQuerySubDir)
	if err != nil {
		errPath := displayPathFromService // Use the validated display path for error messages
		if errPath == "" || errPath == "." {
			errPath = "/"
		}

		if errors.Is(err, os.ErrNotExist) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("Directory not found: %s", errPath))
		}
		if errors.Is(err, os.ErrPermission) {
			return nil, status.Error(codes.PermissionDenied, fmt.Sprintf("Access to directory denied: %s", errPath))
		}
		// Fallback for other errors from ListDirectory
		// log.Printf("Internal ListDirectory error: %v", err)
		return nil, status.Error(codes.Internal, "Failed to read directory: "+err.Error())
	}

	// Adapt service response to pb.ListDirectoryResponse
	var entries []*pb.DirectoryEntry
	for _, se := range serviceEntries {
		// Need to take pointers for proto string fields
		entryName := se.Name
		entryType := se.Type
		entrySize := se.Size // Already string, service formats it
		entryLink := se.Link // Service provides the direct path string for next request

		pbEntry := &pb.DirectoryEntry{
			Name: &entryName,
			Type: &entryType,
			Link: &entryLink,
		}
		// Only set size if it's not empty (consistent with REST which uses omitempty)
		if entrySize != "" {
			pbEntry.Size = &entrySize
		}
		entries = append(entries, pbEntry)
	}

	// displayPathFromService is already the correct user-facing path
	// serviceParentLink is the direct path string for the parent, or "" if at root.
	// For proto *string, if serviceParentLink is "", &serviceParentLink will point to an empty string.
	// This is acceptable; client can interpret "" as "no parent" or "parent is current/root".
	// If the intent is to omit the field if there's no parent, a nil pointer is needed.
	// service.ListDirectory returns "" for root's parent.
	var parentLinkForProto *string
	if serviceParentLink != "" {
		parentLinkForProto = &serviceParentLink
	} // else it remains nil, which means the field might be omitted in JSON (good)

	response := &pb.ListDirectoryResponse{
		Path:       &displayPathFromService,
		Entries:    entries,
		ParentLink: parentLinkForProto,
	}

	return response, nil
}
