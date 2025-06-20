package handler

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "deploytar/proto/deploytar/proto/fileservice/v1"
)

func TestGRPCIntegration(t *testing.T) {
	tempDir := t.TempDir()

	testDir := filepath.Join(tempDir, "testdir")
	err := os.MkdirAll(testDir, 0755)
	require.NoError(t, err)

	testFile := filepath.Join(testDir, "test.txt")
	err = os.WriteFile(testFile, []byte("test content"), 0644)
	require.NoError(t, err)

	originalDir, err := os.Getwd()
	require.NoError(t, err)
	err = os.Chdir(testDir)
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(originalDir)
		require.NoError(t, err)
	}()

	lis, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer func() {
		if err := lis.Close(); err != nil {
			t.Logf("Failed to close listener: %v", err)
		}
	}()

	grpcServer := grpc.NewServer()
	fileService := NewGRPCListDirectoryServer()
	pb.RegisterFileServiceServer(grpcServer, fileService)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			t.Logf("gRPC server error: %v", err)
		}
	}()
	defer grpcServer.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() {
		if err := conn.Close(); err != nil {
			t.Logf("Failed to close connection: %v", err)
		}
	}()

	client := pb.NewFileServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.ListDirectory(ctx, &pb.ListDirectoryRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.NotNil(t, resp.Path)
	assert.Equal(t, "/", *resp.Path)

	entry := resp.Entries[0]
	assert.NotNil(t, entry.Name)
	assert.Equal(t, "test.txt", *entry.Name)
	assert.NotNil(t, entry.Type)
	assert.Equal(t, "file", *entry.Type)
	assert.NotNil(t, entry.Size)
}
