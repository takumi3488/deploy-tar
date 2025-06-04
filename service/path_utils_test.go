package service_test

import (
	"os"
	"path/filepath"
	"testing"

	"deploytar/service"

	"github.com/stretchr/testify/assert"
)

func TestResolveAndValidatePath(t *testing.T) {
	createTempDir := func(t *testing.T, name string) string {
		t.Helper()
		dir, err := os.MkdirTemp("", name)
		assert.NoError(t, err)
		absDir, err := filepath.Abs(dir)
		assert.NoError(t, err)
		return absDir
	}

	cwd, err := os.Getwd()
	assert.NoError(t, err)
	absCwd, err := filepath.Abs(cwd)
	assert.NoError(t, err)
	parentDir := filepath.Dir(absCwd)

	testServeDir := createTempDir(t, "testServeDirPrefix")
	defer func() {
		if err := os.RemoveAll(testServeDir); err != nil {
			t.Logf("Failed to remove temp directory: %v", err)
		}
	}()

	err = os.MkdirAll(filepath.Join(testServeDir, "allowed"), 0755)
	assert.NoError(t, err)
	outsideServeDir := createTempDir(t, "outsideServeDir")
	defer func() {
		if err := os.RemoveAll(outsideServeDir); err != nil {
			t.Logf("Failed to remove temp directory: %v", err)
		}
	}()

	tests := []struct {
		name                string
		rawQuerySubDir      string
		pathPrefixEnv       string
		expectedDisplayPath string
		expectedTargetDir   string
		expectedErr         string
		setup               func(*testing.T) (string, func())
	}{
		{
			name:                "No PATH_PREFIX, simple valid path",
			rawQuerySubDir:      "somedir",
			pathPrefixEnv:       "",
			expectedTargetDir:   filepath.Join(absCwd, "somedir"),
			expectedDisplayPath: "/somedir",
			expectedErr:         "",
		},
		{
			name:                "No PATH_PREFIX, empty rawQuerySubDir",
			rawQuerySubDir:      "",
			pathPrefixEnv:       "",
			expectedTargetDir:   absCwd,
			expectedDisplayPath: "/",
			expectedErr:         "",
		},
		{
			name:                "No PATH_PREFIX, dot rawQuerySubDir",
			rawQuerySubDir:      ".",
			pathPrefixEnv:       "",
			expectedTargetDir:   absCwd,
			expectedDisplayPath: "/",
			expectedErr:         "",
		},
		{
			name:                "No PATH_PREFIX, slash rawQuerySubDir",
			rawQuerySubDir:      "/",
			pathPrefixEnv:       "",
			expectedTargetDir:   absCwd,
			expectedDisplayPath: "/",
			expectedErr:         "",
		},
		{
			name:                "No PATH_PREFIX, path with spaces",
			rawQuerySubDir:      "some dir with spaces",
			pathPrefixEnv:       "",
			expectedTargetDir:   filepath.Join(absCwd, "some dir with spaces"),
			expectedDisplayPath: "/some dir with spaces",
			expectedErr:         "",
		},
		{
			name:           "No PATH_PREFIX, traversal attempt",
			rawQuerySubDir: "../somedir",
			pathPrefixEnv:  "",
			expectedErr:    "access to the requested path is forbidden (resolved path outside CWD)",
		},
		{
			name:                "No PATH_PREFIX, absolute path traversal attempt",
			rawQuerySubDir:      parentDir,
			pathPrefixEnv:       "",
			expectedTargetDir:   filepath.Join(absCwd, parentDir),
			expectedDisplayPath: parentDir,
			expectedErr:         "",
		},
		{
			name:                "With PATH_PREFIX, valid subpath",
			rawQuerySubDir:      "allowed",
			pathPrefixEnv:       testServeDir,
			expectedTargetDir:   filepath.Clean(filepath.Join(testServeDir, "allowed")),
			expectedDisplayPath: "/allowed",
			expectedErr:         "",
		},
		{
			name:                "With PATH_PREFIX, rawQuerySubDir is /",
			rawQuerySubDir:      "/",
			pathPrefixEnv:       testServeDir,
			expectedTargetDir:   filepath.Clean(testServeDir),
			expectedDisplayPath: "/",
			expectedErr:         "",
		},
		{
			name:                "With PATH_PREFIX, rawQuerySubDir is empty",
			rawQuerySubDir:      "",
			pathPrefixEnv:       testServeDir,
			expectedTargetDir:   filepath.Clean(testServeDir),
			expectedDisplayPath: "/",
			expectedErr:         "",
		},
		{
			name:                "With PATH_PREFIX, rawQuerySubDir is dot",
			rawQuerySubDir:      ".",
			pathPrefixEnv:       testServeDir,
			expectedTargetDir:   filepath.Clean(testServeDir),
			expectedDisplayPath: "/",
			expectedErr:         "",
		},
		{
			name:           "With PATH_PREFIX, traversal from subpath",
			rawQuerySubDir: "allowed/../..",
			pathPrefixEnv:  testServeDir,
			expectedErr:    "access to the requested path is forbidden (path traversal attempt?)",
		},
		{
			name:           "With PATH_PREFIX, direct traversal attempt",
			rawQuerySubDir: "../anything",
			pathPrefixEnv:  testServeDir,
			expectedErr:    "access to the requested path is forbidden (path traversal attempt?)",
		},
		{
			name:           "With PATH_PREFIX, absolute path outside prefix",
			rawQuerySubDir: "/tmp/outside_prefix",
			pathPrefixEnv:  testServeDir,
			expectedErr:    "access to the requested path is forbidden (path traversal attempt?)",
		},
		{
			name:                "With PATH_PREFIX, absolute path same as prefix",
			rawQuerySubDir:      testServeDir,
			pathPrefixEnv:       testServeDir,
			expectedTargetDir:   filepath.Clean(testServeDir),
			expectedDisplayPath: "/",
			expectedErr:         "",
		},
		{
			name:                "With PATH_PREFIX, absolute path that is a subpath of prefix",
			rawQuerySubDir:      filepath.Join(testServeDir, "allowed"),
			pathPrefixEnv:       testServeDir,
			expectedTargetDir:   filepath.Clean(filepath.Join(testServeDir, "allowed")),
			expectedDisplayPath: "/allowed",
			expectedErr:         "",
		},
		{
			name:           "PATH_PREFIX is a file, not a directory",
			rawQuerySubDir: "somedir",
			setup: func(t *testing.T) (string, func()) {
				filePrefix, err := os.CreateTemp("", "fileprefix")
				assert.NoError(t, err)
				if _, err := filePrefix.WriteString("iamfile"); err != nil {
					t.Fatalf("Failed to write to temp file: %v", err)
				}
				if err := filePrefix.Close(); err != nil {
					t.Fatalf("Failed to close temp file: %v", err)
				}
				absFilePrefix, _ := filepath.Abs(filePrefix.Name())
				return absFilePrefix, func() {
					if err := os.Remove(filePrefix.Name()); err != nil {
						t.Logf("Failed to remove temp file: %v", err)
					}
				}
			},
			expectedErr: "is not a directory",
		},
		{
			name:           "PATH_PREFIX does not exist",
			rawQuerySubDir: "somedir",
			pathPrefixEnv:  "/non/existent/prefix_12345",
			expectedErr:    "not found",
		},
		{
			name:                "No PATH_PREFIX, complex path needing cleaning",
			rawQuerySubDir:      "some/../some/otherdir",
			pathPrefixEnv:       "",
			expectedTargetDir:   filepath.Join(absCwd, "some", "otherdir"),
			expectedDisplayPath: "/some/otherdir",
			expectedErr:         "",
		},
		{
			name:           "With PATH_PREFIX, complex path needing cleaning",
			rawQuerySubDir: "some/../some/allowedsubdir",
			pathPrefixEnv:  testServeDir,
			setup: func(t *testing.T) (string, func()) {
				err := os.MkdirAll(filepath.Join(testServeDir, "some", "allowedsubdir"), 0755)
				assert.NoError(t, err)
				return testServeDir, func() {}
			},
			expectedTargetDir:   filepath.Join(testServeDir, "some", "allowedsubdir"),
			expectedDisplayPath: "/some/allowedsubdir",
			expectedErr:         "",
		},
		{
			name:                "Path with trailing slash",
			rawQuerySubDir:      "adir/",
			pathPrefixEnv:       "",
			expectedTargetDir:   filepath.Join(absCwd, "adir"),
			expectedDisplayPath: "/adir",
			expectedErr:         "",
		},
		{
			name:                "Path is just /",
			rawQuerySubDir:      "/",
			pathPrefixEnv:       "",
			expectedTargetDir:   absCwd,
			expectedDisplayPath: "/",
			expectedErr:         "",
		},
		{
			name:                "With PATH_PREFIX, rawQuerySubDir is / and prefix has trailing slash",
			rawQuerySubDir:      "/",
			pathPrefixEnv:       testServeDir + "/",
			expectedTargetDir:   filepath.Clean(testServeDir),
			expectedDisplayPath: "/",
			expectedErr:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentPathPrefix := tt.pathPrefixEnv
			var cleanupFunc func()
			if tt.setup != nil {
				var setupPrefix string
				setupPrefix, cleanupFunc = tt.setup(t)
				if cleanupFunc != nil {
					defer cleanupFunc()
				}
				if setupPrefix != "" {
					currentPathPrefix = setupPrefix
				}
			}

			targetDir, displayPath, err := service.ResolveAndValidatePath(tt.rawQuerySubDir, currentPathPrefix)

			if tt.expectedErr != "" {
				assert.Error(t, err)
				if err != nil {
					assert.Contains(t, err.Error(), tt.expectedErr, "Error message mismatch")
				}
			} else {
				assert.NoError(t, err, "Expected no error")
				assert.Equal(t, filepath.Clean(tt.expectedTargetDir), targetDir, "TargetDir mismatch")
				assert.Equal(t, tt.expectedDisplayPath, displayPath, "DisplayPath mismatch")
			}
		})
	}
}
