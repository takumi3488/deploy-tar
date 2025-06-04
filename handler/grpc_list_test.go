package handler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "deploytar/proto/deploytar/proto/fileservice/v1"
)

func TestGRPCListDirectoryServer_ListDirectory(t *testing.T) {
	tempDir := t.TempDir()

	testDir := filepath.Join(tempDir, "testdir")
	err := os.MkdirAll(testDir, 0755)
	require.NoError(t, err)

	subDir := filepath.Join(testDir, "subdir")
	err = os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	testFile := filepath.Join(testDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	subFile := filepath.Join(subDir, "subfile.txt")
	err = os.WriteFile(subFile, []byte("sub content"), 0644)
	require.NoError(t, err)

	originalDir, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(testDir)
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(originalDir)
		require.NoError(t, err)
	}()

	server := NewGRPCListDirectoryServer()

	tests := []struct {
		name        string
		request     *pb.ListDirectoryRequest
		wantError   bool
		errorCode   codes.Code
		checkResult func(t *testing.T, resp *pb.ListDirectoryResponse)
	}{
		{
			name:    "list root directory",
			request: &pb.ListDirectoryRequest{},
			checkResult: func(t *testing.T, resp *pb.ListDirectoryResponse) {
				assert.NotNil(t, resp.Path)
				assert.Equal(t, "/", *resp.Path)

				foundSubdir := false
				foundFile := false
				for _, entry := range resp.Entries {
					assert.NotNil(t, entry.Name)
					assert.NotNil(t, entry.Type)
					switch *entry.Name {
					case "subdir":
						assert.Equal(t, "directory", *entry.Type)
						foundSubdir = true
					case "test.txt":
						assert.Equal(t, "file", *entry.Type)
						assert.NotNil(t, entry.Size)
						foundFile = true
					}
				}
				assert.True(t, foundSubdir)
				assert.True(t, foundFile)
			},
		},
		{
			name: "list subdirectory",
			request: &pb.ListDirectoryRequest{
				Directory: stringPtr("subdir"),
			},
			checkResult: func(t *testing.T, resp *pb.ListDirectoryResponse) {
				assert.NotNil(t, resp.Path)

				entry := resp.Entries[0]
				assert.NotNil(t, entry.Name)
				assert.Equal(t, "subfile.txt", *entry.Name)
				assert.NotNil(t, entry.Type)
				assert.Equal(t, "file", *entry.Type)
			},
		},
		{
			name: "list non-existent directory",
			request: &pb.ListDirectoryRequest{
				Directory: stringPtr("nonexistent"),
			},
			wantError: true,
			errorCode: codes.NotFound,
		},
		{
			name: "path traversal attempt",
			request: &pb.ListDirectoryRequest{
				Directory: stringPtr("../"),
			},
			wantError: true,
			errorCode: codes.PermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.ListDirectory(context.Background(), tt.request)

			if tt.wantError {
				assert.Error(t, err)
				st, ok := status.FromError(err)
				assert.True(t, ok)
				assert.Equal(t, tt.errorCode, st.Code())
			} else {
				assert.NoError(t, err)
				require.NotNil(t, resp)
				if tt.checkResult != nil {
					tt.checkResult(t, resp)
				}
			}
		})
	}
}

func TestGRPCListDirectoryServer_WithPathPrefix(t *testing.T) {
	tempDir := t.TempDir()

	allowedDir := filepath.Join(tempDir, "allowed")
	err := os.MkdirAll(allowedDir, 0755)
	require.NoError(t, err)

	restrictedDir := filepath.Join(tempDir, "restricted")
	err = os.MkdirAll(restrictedDir, 0755)
	require.NoError(t, err)

	testFile := filepath.Join(allowedDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	originalPrefix := os.Getenv("PATH_PREFIX")
	err = os.Setenv("PATH_PREFIX", allowedDir)
	require.NoError(t, err)
	defer func() {
		err := os.Setenv("PATH_PREFIX", originalPrefix)
		require.NoError(t, err)
	}()

	server := NewGRPCListDirectoryServer()

	tests := []struct {
		name        string
		request     *pb.ListDirectoryRequest
		wantError   bool
		errorCode   codes.Code
		checkResult func(t *testing.T, resp *pb.ListDirectoryResponse)
	}{
		{
			name:    "list allowed directory",
			request: &pb.ListDirectoryRequest{},
			checkResult: func(t *testing.T, resp *pb.ListDirectoryResponse) {
				assert.NotNil(t, resp.Path)
				assert.Equal(t, "/", *resp.Path)
			},
		},
		{
			name: "attempt to access restricted path",
			request: &pb.ListDirectoryRequest{
				Directory: stringPtr("../restricted"),
			},
			wantError: true,
			errorCode: codes.PermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.ListDirectory(context.Background(), tt.request)

			if tt.wantError {
				assert.Error(t, err)
				st, ok := status.FromError(err)
				assert.True(t, ok)
				assert.Equal(t, tt.errorCode, st.Code())
			} else {
				assert.NoError(t, err)
				require.NotNil(t, resp)
				if tt.checkResult != nil {
					tt.checkResult(t, resp)
				}
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}
