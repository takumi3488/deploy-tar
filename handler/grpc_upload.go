package handler

import (
	"deploytar/service"
	"fmt"
	"io"
	"os"
	"strings"

	pb "deploytar/proto/deploytar/proto/fileservice/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *GRPCListDirectoryServer) UploadFile(stream pb.FileService_UploadFileServer) error {
	req, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return status.Error(codes.InvalidArgument, "No FileInfo received")
		}
		return status.Errorf(codes.Internal, "Failed to receive initial request: %v", err)
	}

	fileInfo := req.GetInfo()
	if fileInfo == nil {
		return status.Error(codes.InvalidArgument, "Missing FileInfo in the first message")
	}
	if fileInfo.GetFilename() == "" {
		return status.Error(codes.InvalidArgument, "Filename is required in FileInfo")
	}
	if fileInfo.GetPath() == "" {
		return status.Error(codes.InvalidArgument, "Target path is required in FileInfo")
	}

	targetDirUserPath := fileInfo.GetPath()
	fileName := fileInfo.GetFilename()
	pathPrefixEnv := os.Getenv("PATH_PREFIX")

	tempFile, err := os.CreateTemp("", "grpc-upload-*.tmp")
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to create temporary file: %v", err)
	}

	defer func() {
		if err := os.Remove(tempFile.Name()); err != nil {
			_ = err
		}
	}()

	for {
		chunkReq, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if cerr := tempFile.Close(); cerr != nil {
				_ = cerr
			}
			return status.Errorf(codes.Internal, "Failed to receive file chunk: %v", err)
		}

		if chunkReq.GetInfo() != nil {
			if cerr := tempFile.Close(); cerr != nil {
				_ = cerr
			}
			return status.Error(codes.InvalidArgument, "Received FileInfo message after the first one")
		}

		chunkData := chunkReq.GetChunkData()
		if _, err := tempFile.Write(chunkData); err != nil {
			if cerr := tempFile.Close(); cerr != nil {
				_ = cerr
			}
			return status.Errorf(codes.Internal, "Failed to write to temporary file: %v", err)
		}
	}

	if err := tempFile.Sync(); err != nil {
		if cerr := tempFile.Close(); cerr != nil {
			_ = cerr
		}
		return status.Errorf(codes.Internal, "Failed to sync temporary file: %v", err)
	}
	if err := tempFile.Close(); err != nil {
		return status.Errorf(codes.Internal, "Failed to close temporary file before processing: %v", err)
	}

	readOnlyTempFile, err := os.Open(tempFile.Name())
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to re-open temporary file for reading: %v", err)
	}
	defer func() {
		if err := readOnlyTempFile.Close(); err != nil {
			_ = err
		}
	}()

	finalPath, serviceErr := service.UploadFile(readOnlyTempFile, targetDirUserPath, fileName, pathPrefixEnv, true)
	if serviceErr != nil {
		errMsg := serviceErr.Error()
		if strings.Contains(errMsg, "forbidden") ||
			strings.Contains(errMsg, "traversal") ||
			strings.Contains(errMsg, "outside the scope") ||
			strings.Contains(errMsg, "unsafe path") ||
			strings.Contains(errMsg, "cannot be a path traversal attempt") {
			return status.Error(codes.PermissionDenied, errMsg)
		}
		if strings.Contains(errMsg, "not found") ||
			strings.Contains(errMsg, "does not exist") {
			return status.Error(codes.NotFound, errMsg)
		}
		if strings.Contains(errMsg, "archive") ||
			strings.Contains(errMsg, "gzipped content") ||
			strings.Contains(errMsg, "file content") ||
			strings.Contains(errMsg, "Failed to create gzip reader") ||
			strings.Contains(errMsg, "is not a directory") {
			return status.Error(codes.InvalidArgument, errMsg)
		}
		return status.Error(codes.Internal, "Failed to process file upload: "+errMsg)
	}

	msg := fmt.Sprintf("File '%s' processed successfully, final path: %s", fileName, finalPath)
	finalPathProto := finalPath

	return stream.SendAndClose(&pb.UploadFileResponse{
		Message:  &msg,
		FilePath: &finalPathProto,
	})
}
