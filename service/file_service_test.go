package service_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"deploytar/service"
)

func setupTestFs(t *testing.T) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "test_list_dir_*")
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "file1.txt"), make([]byte, 10), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "z_file_last.txt"), make([]byte, 5), 0644)
	require.NoError(t, err)
	dir1 := filepath.Join(tmpDir, "dir1")
	err = os.Mkdir(dir1, 0755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir1, "file2.txt"), make([]byte, 20), 0644)
	require.NoError(t, err)
	emptyDir := filepath.Join(tmpDir, "empty_dir")
	err = os.Mkdir(emptyDir, 0755)
	require.NoError(t, err)
	aDirFirst := filepath.Join(tmpDir, "a_dir_first")
	err = os.Mkdir(aDirFirst, 0755)
	require.NoError(t, err)

	t.Cleanup(func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp directory: %v", err)
		}
	})
	return tmpDir
}

func findEntry(entries []service.DirectoryEntryService, name string) (service.DirectoryEntryService, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return service.DirectoryEntryService{}, false
}

func TestListDirectory(t *testing.T) {
	testRootDir := setupTestFs(t)

	t.Run("list root directory with originalRequestPath /", func(t *testing.T) {
		entries, parentLink, err := service.ListDirectory(testRootDir, "/")
		require.NoError(t, err)
		assert.Equal(t, "", parentLink)
		require.Len(t, entries, 5)
		expectedEntries := map[string]service.DirectoryEntryService{
			"file1.txt":       {Name: "file1.txt", Type: "file", Size: "10 B", Link: "/file1.txt"},
			"z_file_last.txt": {Name: "z_file_last.txt", Type: "file", Size: "5 B", Link: "/z_file_last.txt"},
			"dir1":            {Name: "dir1", Type: "directory", Size: "", Link: "/dir1"},
			"empty_dir":       {Name: "empty_dir", Type: "directory", Size: "", Link: "/empty_dir"},
			"a_dir_first":     {Name: "a_dir_first", Type: "directory", Size: "", Link: "/a_dir_first"},
		}
		for name, expected := range expectedEntries {
			found, ok := findEntry(entries, name)
			require.True(t, ok, "Entry %s not found", name)
			assert.Equal(t, expected, found, "Entry mismatch for %s", name)
		}
	})
	t.Run("list root directory with originalRequestPath .", func(t *testing.T) {
		entries, parentLink, err := service.ListDirectory(testRootDir, ".")
		require.NoError(t, err)
		assert.Equal(t, "", parentLink)
		require.Len(t, entries, 5)
		entryFile1, ok := findEntry(entries, "file1.txt")
		require.True(t, ok)
		assert.Equal(t, "/file1.txt", entryFile1.Link)
	})
	t.Run("list root directory with originalRequestPath empty", func(t *testing.T) {
		entries, parentLink, err := service.ListDirectory(testRootDir, "")
		require.NoError(t, err)
		assert.Equal(t, "", parentLink)
		require.Len(t, entries, 5)
		entryDir1, ok := findEntry(entries, "dir1")
		require.True(t, ok)
		assert.Equal(t, "/dir1", entryDir1.Link)
	})
	t.Run("list subdirectory dir1", func(t *testing.T) {
		absPathToDir1 := filepath.Join(testRootDir, "dir1")
		entries, parentLink, err := service.ListDirectory(absPathToDir1, "/dir1")
		require.NoError(t, err)
		assert.Equal(t, "/", parentLink)
		require.Len(t, entries, 1)
		expected := service.DirectoryEntryService{Name: "file2.txt", Type: "file", Size: "20 B", Link: "/dir1/file2.txt"}
		assert.Equal(t, expected, entries[0])
	})
	t.Run("list subdirectory dir1 with trailing slash in originalRequestPath", func(t *testing.T) {
		absPathToDir1 := filepath.Join(testRootDir, "dir1")
		entries, parentLink, err := service.ListDirectory(absPathToDir1, "/dir1/")
		require.NoError(t, err)
		assert.Equal(t, "/", parentLink)
		require.Len(t, entries, 1)
		assert.Equal(t, "/dir1/file2.txt", entries[0].Link)
	})
	t.Run("list empty directory", func(t *testing.T) {
		absPathToEmptyDir := filepath.Join(testRootDir, "empty_dir")
		entries, parentLink, err := service.ListDirectory(absPathToEmptyDir, "/empty_dir")
		require.NoError(t, err)
		assert.Equal(t, "/", parentLink)
		assert.Empty(t, entries)
	})
	t.Run("directory not found", func(t *testing.T) {
		_, _, err := service.ListDirectory(filepath.Join(testRootDir, "non_existent_dir"), "/non_existent_dir")
		assert.Error(t, err)
		isNotExist := errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrNotExist)
		if !isNotExist {
			assert.Contains(t, err.Error(), "no such file or directory")
		} else {
			assert.True(t, isNotExist)
		}
	})
	t.Run("list a_dir_first to check parent link logic deeply", func(t *testing.T) {
		absPathToADirFirst := filepath.Join(testRootDir, "a_dir_first")
		_, parentLink, err := service.ListDirectory(absPathToADirFirst, "/a_dir_first")
		require.NoError(t, err)
		assert.Equal(t, "/", parentLink)
	})
	t.Run("list root with non-slash originalRequestPath", func(t *testing.T) {
		absPathToADirFirst := filepath.Join(testRootDir, "a_dir_first")
		err := os.WriteFile(filepath.Join(absPathToADirFirst, "child.txt"), make([]byte, 5), 0644)
		require.NoError(t, err)
		entries, parentLink, errList := service.ListDirectory(absPathToADirFirst, "a_dir_first")
		require.NoError(t, errList)
		assert.Equal(t, "/", parentLink)
		require.Len(t, entries, 1)
		assert.Equal(t, "/a_dir_first/child.txt", entries[0].Link)
	})
}
func TestFormatFileSizeServiceInternal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_format_size_*")
	require.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp directory: %v", err)
		}
	}()
	kibPath := filepath.Join(tmpDir, "kib.txt")
	err = os.WriteFile(kibPath, make([]byte, 1024), 0644)
	require.NoError(t, err)
	mibPath := filepath.Join(tmpDir, "mib.txt")
	err = os.WriteFile(mibPath, make([]byte, 1024*1024), 0644)
	require.NoError(t, err)
	t.Run("formatFileSize via ListDirectory", func(t *testing.T) {
		entries, _, err := service.ListDirectory(tmpDir, "/")
		require.NoError(t, err)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		require.Len(t, entries, 2)
		assert.Equal(t, "kib.txt", entries[0].Name)
		assert.Equal(t, "1.0 KiB", entries[0].Size)
		assert.Equal(t, "mib.txt", entries[1].Name)
		assert.Equal(t, "1.0 MiB", entries[1].Size)
	})
}
func TestGetFileInfoService_Symlink(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test_symlink_*")
	require.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp directory: %v", err)
		}
	}()
	filePath := filepath.Join(tmpDir, "actual_file.txt")
	err = os.WriteFile(filePath, []byte("hello"), 0644)
	require.NoError(t, err)
	symlinkPath := filepath.Join(tmpDir, "symlink_to_file")
	err = os.Symlink(filePath, symlinkPath)
	require.NoError(t, err)
	brokenSymlinkPath := filepath.Join(tmpDir, "broken_symlink")
	nonExistentTarget := filepath.Join(tmpDir, "non_existent_target.txt")
	err = os.Symlink(nonExistentTarget, brokenSymlinkPath)
	require.NoError(t, err)
	t.Run("get info for a valid symlink", func(t *testing.T) {
		entries, _, errList := service.ListDirectory(tmpDir, "/")
		require.NoError(t, errList)
		foundSymlink, ok := findEntry(entries, "symlink_to_file")
		require.True(t, ok)
		assert.Equal(t, "file", foundSymlink.Type)
		assert.Equal(t, "5 B", foundSymlink.Size)
	})
	t.Run("get info for a broken symlink", func(t *testing.T) {
		entries, _, errList := service.ListDirectory(tmpDir, "/")
		require.NoError(t, errList)
		_, okBroken := findEntry(entries, "broken_symlink")
		assert.False(t, okBroken)
	})
}

func createTestTar(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0600, Size: int64(len(content))}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf
}

func createTestTarGz(t *testing.T, files map[string]string) *bytes.Buffer {
	t.Helper()
	tarBuf := createTestTar(t, files)
	gzBuf := new(bytes.Buffer)
	gzw := gzip.NewWriter(gzBuf)
	_, err := gzw.Write(tarBuf.Bytes())
	require.NoError(t, err)
	require.NoError(t, gzw.Close())
	return gzBuf
}

func createTestGz(t *testing.T, content string) *bytes.Buffer {
	t.Helper()
	buf := new(bytes.Buffer)
	gzw := gzip.NewWriter(buf)
	_, err := gzw.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, gzw.Close())
	return buf
}

func TestUploadFile(t *testing.T) {
	baseUploadDir, err := os.MkdirTemp("", "upload_test_base_*")
	require.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(baseUploadDir); err != nil {
			t.Logf("Failed to remove temp directory: %v", err)
		}
	}()

	prefixDir := filepath.Join(baseUploadDir, "prefix_uploads")
	err = os.Mkdir(prefixDir, 0755)
	require.NoError(t, err)

	tests := []struct {
		name                string
		inputStream         io.Reader
		targetDirUserPath   string
		fileName            string
		isPutRequest        bool
		expectErrorContains string
		pathPrefixEnv       string
		expectedFinalPath   func(string) string
		expectedContent     map[string]string
	}{
		{
			name:              "upload plain file, no prefix",
			inputStream:       strings.NewReader("hello world"),
			targetDirUserPath: "plain_target",
			fileName:          "test.txt",
			pathPrefixEnv:     "",
			isPutRequest:      false,
			expectedFinalPath: func(uploadDir string) string { return filepath.Join(uploadDir, "plain_target", "test.txt") },
			expectedContent:   map[string]string{"test.txt": "hello world"},
		},
		{
			name:              "upload plain file with subdirs in filename, no prefix",
			inputStream:       strings.NewReader("subdir content"),
			targetDirUserPath: "plain_target_subdir",
			fileName:          "subdir/test.txt",
			pathPrefixEnv:     "",
			isPutRequest:      false,
			expectedFinalPath: func(uploadDir string) string {
				return filepath.Join(uploadDir, "plain_target_subdir", "subdir/test.txt")
			},
			expectedContent: map[string]string{"subdir/test.txt": "subdir content"},
		},
		{
			name:          "upload plain file with prefix",
			inputStream:   strings.NewReader("hello prefix"),
			fileName:      "test_with_prefix.txt",
			pathPrefixEnv: prefixDir,
			isPutRequest:  false,
			expectedFinalPath: func(_ string) string {
				return filepath.Join(prefixDir, "plain_target_with_prefix", "test_with_prefix.txt")
			},
			expectedContent: map[string]string{"test_with_prefix.txt": "hello prefix"},
		},
		{
			name:              "upload .tar, no prefix",
			inputStream:       createTestTar(t, map[string]string{"file_in_tar.txt": "tar content", "d/f.txt": "deep"}),
			targetDirUserPath: "tar_target",
			fileName:          "archive.tar",
			pathPrefixEnv:     "",
			isPutRequest:      false,
			expectedContent:   map[string]string{"file_in_tar.txt": "tar content", "d/f.txt": "deep"},
		},
		{
			name:              "upload .tar.gz, with prefix",
			inputStream:       createTestTarGz(t, map[string]string{"file_in_tgz.txt": "tgz content"}),
			fileName:          "archive.tar.gz",
			pathPrefixEnv:     prefixDir,
			isPutRequest:      false,
			expectedFinalPath: func(_ string) string { return filepath.Join(prefixDir, "tgz_target") },
			expectedContent:   map[string]string{"file_in_tgz.txt": "tgz content"},
		},
		{
			name:              "upload single .gz, no prefix",
			inputStream:       createTestGz(t, "gzipped content"),
			targetDirUserPath: "gz_single_target",
			fileName:          "single.txt.gz",
			pathPrefixEnv:     "",
			isPutRequest:      false,
			expectedFinalPath: func(uploadDir string) string { return filepath.Join(uploadDir, "gz_single_target", "single.txt") },
			expectedContent:   map[string]string{"single.txt": "gzipped content"},
		},
		{
			name:              "upload plain file with PUT, no prefix",
			inputStream:       strings.NewReader("put content"),
			targetDirUserPath: "put_target",
			fileName:          "put_test.txt",
			pathPrefixEnv:     "",
			isPutRequest:      true,
			expectedFinalPath: func(uploadDir string) string { return filepath.Join(uploadDir, "put_target", "put_test.txt") },
		},
		{
			name:                "upload plain file, target path traversal",
			inputStream:         strings.NewReader(""),
			targetDirUserPath:   "../bad_target",
			fileName:            "test.txt",
			pathPrefixEnv:       "",
			isPutRequest:        false,
			expectErrorContains: "target directory cannot be a path traversal attempt:",
		},
		{
			name:                "upload plain file, target path traversal with prefix",
			inputStream:         strings.NewReader(""),
			targetDirUserPath:   "../target_outside_prefix",
			fileName:            "test.txt",
			pathPrefixEnv:       prefixDir,
			isPutRequest:        false,
			expectErrorContains: "target directory cannot be a path traversal attempt:",
		},
		{
			name:                "upload plain file, absolute target path outside prefix",
			inputStream:         strings.NewReader(""),
			targetDirUserPath:   "/tmp/absolute_path",
			fileName:            "test.txt",
			pathPrefixEnv:       prefixDir,
			isPutRequest:        false,
			expectErrorContains: "is outside the scope of path prefix",
		},
		{
			name:                "upload .tar with traversal in header name",
			inputStream:         createTestTar(t, map[string]string{"../../evil.txt": "evil content"}),
			targetDirUserPath:   "tar_evil_target",
			fileName:            "evil.tar",
			pathPrefixEnv:       "",
			isPutRequest:        false,
			expectErrorContains: "contains potentially unsafe path entry",
		},
		{
			name:                "upload .tar with absolute path in header name",
			inputStream:         createTestTar(t, map[string]string{filepath.Join(baseUploadDir, "abs_evil.txt"): "evil content"}),
			targetDirUserPath:   "tar_abs_evil_target",
			fileName:            "abs_evil.tar",
			pathPrefixEnv:       "",
			isPutRequest:        false,
			expectErrorContains: "contains potentially unsafe path entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set working directory to base upload dir for proper path validation
			originalWd, err := os.Getwd()
			require.NoError(t, err)
			err = os.Chdir(baseUploadDir)
			require.NoError(t, err)
			defer func() {
				if err := os.Chdir(originalWd); err != nil {
					t.Logf("Failed to restore working directory: %v", err)
				}
			}()

			var callTargetDirUserPath string
			currentTestUploadBase := baseUploadDir

			if tt.pathPrefixEnv == "" {
				if tt.expectErrorContains != "" {
					// For error test cases, use the target path as-is to test validation
					callTargetDirUserPath = tt.targetDirUserPath
				} else {
					uniqueTestDir := filepath.Join(currentTestUploadBase, strings.ReplaceAll(tt.name, " ", "_"))
					err := os.MkdirAll(uniqueTestDir, 0755)
					require.NoError(t, err)
					if tt.targetDirUserPath != "" {
						callTargetDirUserPath = filepath.Join(uniqueTestDir, tt.targetDirUserPath)
					} else {
						callTargetDirUserPath = uniqueTestDir
					}
				}
			} else {
				callTargetDirUserPath = tt.targetDirUserPath
			}

			if tt.name == "upload plain file with PUT, no prefix" {
				putTargetDirForSetup := filepath.Clean(callTargetDirUserPath)
				err := os.MkdirAll(putTargetDirForSetup, 0755)
				require.NoError(t, err)
				err = os.WriteFile(filepath.Join(putTargetDirForSetup, "old_file.txt"), []byte("old"), 0644)
				require.NoError(t, err)
			}

			actualFinalPath, err := service.UploadFile(tt.inputStream, callTargetDirUserPath, tt.fileName, tt.pathPrefixEnv, tt.isPutRequest)

			t.Logf("Test: %s, callTargetDirUserPath: %s, err: %v", tt.name, callTargetDirUserPath, err)

			if tt.expectErrorContains != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErrorContains)
			} else {
				require.NoError(t, err, "UploadFile failed unexpectedly")

				var expectedAbsFinalPath string
				if strings.Contains(tt.fileName, ".tar") || strings.Contains(tt.fileName, ".tgz") {
					if tt.pathPrefixEnv != "" && !filepath.IsAbs(callTargetDirUserPath) {
						expectedAbsFinalPath = filepath.Join(tt.pathPrefixEnv, callTargetDirUserPath)
					} else {
						expectedAbsFinalPath = callTargetDirUserPath
					}
				} else {
					var finalNamePart = tt.fileName
					if strings.HasSuffix(strings.ToLower(tt.fileName), ".gz") && !strings.HasSuffix(strings.ToLower(tt.fileName), ".tar.gz") {
						finalNamePart = strings.TrimSuffix(tt.fileName, ".gz")
						if finalNamePart == "" {
							finalNamePart = "gzipped_file"
						}
					}
					if tt.pathPrefixEnv != "" && !filepath.IsAbs(callTargetDirUserPath) {
						expectedAbsFinalPath = filepath.Join(tt.pathPrefixEnv, callTargetDirUserPath, finalNamePart)
					} else {
						expectedAbsFinalPath = filepath.Join(callTargetDirUserPath, finalNamePart)
					}
				}
				assert.Equal(t, filepath.Clean(expectedAbsFinalPath), filepath.Clean(actualFinalPath), "Final path mismatch")

				for relPathOrFileName, expectedText := range tt.expectedContent {
					var contentFilePath string
					if strings.Contains(tt.fileName, ".tar") || strings.Contains(tt.fileName, ".tgz") {
						contentFilePath = filepath.Join(actualFinalPath, relPathOrFileName)
					} else {
						contentFilePath = actualFinalPath
					}
					contentBytes, errRead := os.ReadFile(contentFilePath)
					require.NoError(t, errRead, "Failed to read expected file: %s (rel: %s)", contentFilePath, relPathOrFileName)
					assert.Equal(t, expectedText, string(contentBytes), "Content mismatch for: %s", relPathOrFileName)
				}

				if tt.name == "upload plain file with PUT, no prefix" {
					_, errStat := os.Stat(filepath.Join(filepath.Dir(actualFinalPath), "old_file.txt"))
					assert.True(t, os.IsNotExist(errStat), "Old file should be gone after PUT")
				}
			}
		})
	}
}
