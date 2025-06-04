package handler

import (
	"deploytar/service" // Assuming 'deploytar' is the module name
	"fmt"
	"io"
	"os"
	"strings"

	pb "deploytar/proto/deploytar/proto/fileservice/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	// "log" // Example for logging, if needed
)

// UploadFile is the gRPC handler for uploading files.
// It receives a stream of chunks, saves to a temporary file, then calls the service.
func (s *GRPCListDirectoryServer) UploadFile(stream pb.FileService_UploadFileServer) error {
	// First, receive the FileInfo message
	req, err := stream.Recv()
	if err != nil {
		if err == io.EOF { // Should not happen for the first message
			return status.Error(codes.InvalidArgument, "No FileInfo received")
		}
		// log.Printf("Error receiving initial upload request: %v", err)
		return status.Errorf(codes.Internal, "Failed to receive initial request: %v", err)
	}

	fileInfo := req.GetInfo()
	if fileInfo == nil {
		return status.Error(codes.InvalidArgument, "Missing FileInfo in the first message")
	}
	if fileInfo.GetFilename() == "" {
		return status.Error(codes.InvalidArgument, "Filename is required in FileInfo")
	}
	if fileInfo.GetPath() == "" { // Path is the target directory for upload
		return status.Error(codes.InvalidArgument, "Target path is required in FileInfo")
	}

	targetDirUserPath := fileInfo.GetPath()
	fileName := fileInfo.GetFilename()
	pathPrefixEnv := os.Getenv("PATH_PREFIX")

	// Create a temporary file to store the uploaded content
	tempFile, err := os.CreateTemp("", "grpc-upload-*.tmp")
	if err != nil {
		// log.Printf("Failed to create temporary file: %v", err)
		return status.Errorf(codes.Internal, "Failed to create temporary file: %v", err)
	}

	// Defer removal of the temporary file. Close happens before remove.
	defer func() {
		// tempFile.Close() might have already been called explicitly.
		// Closing an already closed file usually returns an error, which we can ignore here
		// if the primary goal is removal. However, ensure it *is* closed before removing.
		// The explicit close before os.Open is more critical.
		// log.Printf("Attempting to remove temp file: %s", tempFile.Name())
		if err := os.Remove(tempFile.Name()); err != nil {
			// log.Printf("Failed to remove temporary file %s: %v", tempFile.Name(), err)
		}
	}()

	// Receive file chunks and write to the temporary file
	for {
		chunkReq, err := stream.Recv()
		if err == io.EOF {
			// EOF signals client has finished sending chunks
			break
		}
		if err != nil {
			// log.Printf("Error receiving file chunk: %v", err)
			// Close tempFile before returning to trigger deferred Remove if possible.
			if cerr := tempFile.Close(); cerr != nil {
				// log.Printf("Failed to close tempFile after stream error: %v", cerr)
			}
			return status.Errorf(codes.Internal, "Failed to receive file chunk: %v", err)
		}

		// Ensure subsequent messages are not FileInfo again (though proto might not allow oneof here)
		if chunkReq.GetInfo() != nil {
			if cerr := tempFile.Close(); cerr != nil {
				// log.Printf("Failed to close tempFile after unexpected FileInfo: %v", cerr)
			}
			return status.Error(codes.InvalidArgument, "Received FileInfo message after the first one")
		}

		chunkData := chunkReq.GetChunkData()
		if _, err := tempFile.Write(chunkData); err != nil {
			// log.Printf("Failed to write to temporary file: %v", err)
			if cerr := tempFile.Close(); cerr != nil {
				// log.Printf("Failed to close tempFile after write error: %v", cerr)
			}
			return status.Errorf(codes.Internal, "Failed to write to temporary file: %v", err)
		}
	}

	// Ensure all data is written to disk and close the file for writing.
	if err := tempFile.Sync(); err != nil {
		// log.Printf("Failed to sync temporary file %s: %v", tempFile.Name(), err)
		if cerr := tempFile.Close(); cerr != nil {
			// log.Printf("Failed to close tempFile after sync error: %v", cerr)
		}
		return status.Errorf(codes.Internal, "Failed to sync temporary file: %v", err)
	}
	if err := tempFile.Close(); err != nil { // Explicit close before re-opening
		// log.Printf("Failed to close temporary file %s before reopening: %v", tempFile.Name(), err)
		return status.Errorf(codes.Internal, "Failed to close temporary file before processing: %v", err)
	}

	// Re-open the temporary file for reading to pass to the service
	readOnlyTempFile, err := os.Open(tempFile.Name())
	if err != nil {
		// log.Printf("Failed to re-open temporary file %s for reading: %v", tempFile.Name(), err)
		return status.Errorf(codes.Internal, "Failed to re-open temporary file for reading: %v", err)
	}
	defer readOnlyTempFile.Close() // This close is for the read-only handle

	// Call the service layer for file upload.
	// For gRPC, UploadFile implies a PUT-like behavior (replace/ensure directory).
	finalPath, serviceErr := service.UploadFile(readOnlyTempFile, targetDirUserPath, fileName, pathPrefixEnv, true)
	if serviceErr != nil {
		errMsg := serviceErr.Error()
		// log.Printf("Service UploadFile error: %s (targetDir: %s, fileName: %s, prefix: %s)", errMsg, targetDirUserPath, fileName, pathPrefixEnv)
		if strings.Contains(errMsg, "forbidden") ||
			strings.Contains(errMsg, "traversal") ||
			strings.Contains(errMsg, "outside the scope") ||
			strings.Contains(errMsg, "unsafe path") ||
			strings.Contains(errMsg, "cannot be a path traversal attempt") {
			return status.Error(codes.PermissionDenied, errMsg)
		}
		if strings.Contains(errMsg, "not found") ||
			strings.Contains(errMsg, "does not exist") { // e.g. PATH_PREFIX dir not found
			return status.Error(codes.NotFound, errMsg)
		}
		if strings.Contains(errMsg, "archive") || // Covers tar/gzip read issues
			strings.Contains(errMsg, "gzipped content") || // Covers bad .gz file
			strings.Contains(errMsg, "file content") || // Covers io.Copy issues for plain files
			strings.Contains(errMsg, "Failed to create gzip reader") || // Specific error from service
			strings.Contains(errMsg, "is not a directory") { // e.g. PATH_PREFIX is a file
			return status.Error(codes.InvalidArgument, errMsg)
		}
		return status.Error(codes.Internal, "Failed to process file upload: "+errMsg)
	}

	// Send response
	msg := fmt.Sprintf("File '%s' processed successfully, final path: %s", fileName, finalPath)
	finalPathProto := finalPath // Already a string

	return stream.SendAndClose(&pb.UploadFileResponse{
		Message:  &msg,
		FilePath: &finalPathProto,
	})
}

// isValidGrpcUploadPath was a helper in the original, now its logic is in service.UploadFile's path validation.
// If specific gRPC-only path validation rules were needed (e.g., disallowing certain characters not handled by service),
// it could be kept or adapted. For now, assuming service layer validation is sufficient.
