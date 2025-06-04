package handler

import (
	"context"
	"deploytar/service"
	"errors"
	"fmt"
	"os"
	"strings"

	pb "deploytar/proto/deploytar/proto/fileservice/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCListDirectoryServer struct {
	pb.UnimplementedFileServiceServer
}

func NewGRPCListDirectoryServer() *GRPCListDirectoryServer {
	return &GRPCListDirectoryServer{}
}

func (s *GRPCListDirectoryServer) ListDirectory(ctx context.Context, req *pb.ListDirectoryRequest) (*pb.ListDirectoryResponse, error) {
	pathPrefixEnv := os.Getenv("PATH_PREFIX")
	rawQuerySubDir := ""
	if req.Directory != nil {
		rawQuerySubDir = req.GetDirectory()
	}

	validatedAbsPath, displayPathFromService, err := service.ResolveAndValidatePath(rawQuerySubDir, pathPrefixEnv)
	if err != nil {
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
		return nil, status.Error(codes.Internal, "Internal server error during path validation: "+errMsg)
	}

	serviceEntries, serviceParentLink, err := service.ListDirectory(validatedAbsPath, rawQuerySubDir)
	if err != nil {
		errPath := displayPathFromService
		if errPath == "" || errPath == "." {
			errPath = "/"
		}

		if errors.Is(err, os.ErrNotExist) {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("Directory not found: %s", errPath))
		}
		if errors.Is(err, os.ErrPermission) {
			return nil, status.Error(codes.PermissionDenied, fmt.Sprintf("Access to directory denied: %s", errPath))
		}
		return nil, status.Error(codes.Internal, "Failed to read directory: "+err.Error())
	}

	var entries []*pb.DirectoryEntry
	for _, se := range serviceEntries {
		entryName := se.Name
		entryType := se.Type
		entrySize := se.Size
		entryLink := se.Link

		pbEntry := &pb.DirectoryEntry{
			Name: &entryName,
			Type: &entryType,
			Link: &entryLink,
		}
		if entrySize != "" {
			pbEntry.Size = &entrySize
		}
		entries = append(entries, pbEntry)
	}

	var parentLinkForProto *string
	if serviceParentLink != "" {
		parentLinkForProto = &serviceParentLink
	}

	response := &pb.ListDirectoryResponse{
		Path:       &displayPathFromService,
		Entries:    entries,
		ParentLink: parentLinkForProto,
	}

	return response, nil
}
