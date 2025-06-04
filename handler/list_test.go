package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func setupTestEnvironment(t *testing.T) (string, func()) {
	t.Helper()
	rootDir, err := os.MkdirTemp("", "list_handler_test_")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	originalWd, err := os.Getwd()
	if err != nil {
		if errRemove := os.RemoveAll(rootDir); errRemove != nil {
			t.Logf("Failed to remove temp dir during setup cleanup: %v", errRemove)
		}
		t.Fatalf("Failed to get current working directory: %v", err)
	}
	err = os.Chdir(rootDir)
	if err != nil {
		if errRemove := os.RemoveAll(rootDir); errRemove != nil {
			t.Logf("Failed to remove temp dir during setup cleanup: %v", errRemove)
		}
		if errChdir := os.Chdir(originalWd); errChdir != nil {
			t.Logf("Failed to change directory back to originalWd during setup cleanup: %v", errChdir)
		}
		t.Fatalf("Failed to change current working directory to %s: %v", rootDir, err)
	}

	err = os.WriteFile(filepath.Join(rootDir, "file1.txt"), []byte("content1"), 0644)
	if err != nil {
		t.Fatalf("Failed to create file1.txt: %v", err)
	}

	dir1 := filepath.Join(rootDir, "dir1")
	err = os.Mkdir(dir1, 0755)
	if err != nil {
		t.Fatalf("Failed to create dir1: %v", err)
	}

	err = os.WriteFile(filepath.Join(dir1, "file2.txt"), []byte("content2"), 0644)
	if err != nil {
		t.Fatalf("Failed to create file2.txt: %v", err)
	}

	emptyDir := filepath.Join(rootDir, "empty_dir")
	err = os.Mkdir(emptyDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create empty_dir: %v", err)
	}

	cleanup := func() {
		errChdirBack := os.Chdir(originalWd)
		if errChdirBack != nil {
			t.Logf("Warning: failed to change directory back to %s: %v", originalWd, errChdirBack)
		}
		errRemoveAll := os.RemoveAll(rootDir)
		if errRemoveAll != nil {
			t.Logf("Warning: failed to remove temp dir %s: %v", rootDir, errRemoveAll)
		}
	}
	return rootDir, cleanup
}

func TestListDirectoryHandler_SuccessCases(t *testing.T) {
	originalWd, _ := os.Getwd()
	testRootDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	err := os.Chdir(testRootDir)
	if err != nil {
		t.Fatalf("Failed to change directory to test root: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Logf("Failed to change directory back to originalWd: %v", err)
		}
	}()

	e := echo.New()

	tests := []struct {
		name               string
		queryD             string
		pathPrefixEnv      string
		expectedStatus     int
		expectedPath       string
		expectedEntryNames []string
		expectedEntryTypes []string
		expectParentLink   bool
		expectedParentLink string
	}{
		{
			name:               "List root directory (no prefix, no query d)",
			queryD:             "",
			pathPrefixEnv:      "",
			expectedStatus:     http.StatusOK,
			expectedPath:       "/",
			expectedEntryNames: []string{"file1.txt", "dir1", "empty_dir"},
			expectedEntryTypes: []string{"file", "directory", "directory"},
			expectParentLink:   false,
		},
		{
			name:               "List subdirectory (no prefix, query d=dir1)",
			queryD:             "dir1",
			pathPrefixEnv:      "",
			expectedStatus:     http.StatusOK,
			expectedPath:       "/dir1",
			expectedEntryNames: []string{"file2.txt"},
			expectedEntryTypes: []string{"file"},
			expectParentLink:   true,
			expectedParentLink: "/list?d=/",
		},
		{
			name:               "List empty directory (no prefix, query d=empty_dir)",
			queryD:             "empty_dir",
			pathPrefixEnv:      "",
			expectedStatus:     http.StatusOK,
			expectedPath:       "/empty_dir",
			expectedEntryNames: []string{},
			expectedEntryTypes: []string{},
			expectParentLink:   true,
			expectedParentLink: "/list?d=/",
		},
		{
			name:               "List root with PATH_PREFIX=/serve (query d is empty)",
			queryD:             "",
			pathPrefixEnv:      "/serve",
			expectedStatus:     http.StatusOK,
			expectedPath:       "/",
			expectedEntryNames: []string{"file1.txt", "dir1", "empty_dir"},
			expectedEntryTypes: []string{"file", "directory", "directory"},
			expectParentLink:   false,
		},
		{
			name:               "List subdir with PATH_PREFIX=/serve, query d=dir1",
			queryD:             "dir1",
			pathPrefixEnv:      "/serve",
			expectedStatus:     http.StatusOK,
			expectedPath:       "/dir1",
			expectedEntryNames: []string{"file2.txt"},
			expectedEntryTypes: []string{"file"},
			expectParentLink:   true,
			expectedParentLink: "/list?d=/",
		},
		{
			name:               "List root with PATH_PREFIX=/ (slash only, query d is empty)",
			queryD:             "",
			pathPrefixEnv:      "/",
			expectedStatus:     http.StatusOK,
			expectedPath:       "/",
			expectedEntryNames: []string{"file1.txt", "dir1", "empty_dir"},
			expectedEntryTypes: []string{"file", "directory", "directory"},
			expectParentLink:   false,
		},
		{
			name:               "List subdir with PATH_PREFIX=/, query d=dir1",
			queryD:             "dir1",
			pathPrefixEnv:      "/",
			expectedStatus:     http.StatusOK,
			expectedPath:       "/dir1",
			expectedEntryNames: []string{"file2.txt"},
			expectedEntryTypes: []string{"file"},
			expectParentLink:   true,
			expectedParentLink: "/list?d=/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentTestPrefixPath := tt.pathPrefixEnv
			var createdPrefixDir string
			if tt.pathPrefixEnv != "" && tt.pathPrefixEnv != "/" {
				prefixDirName := tt.pathPrefixEnv
				if filepath.IsAbs(prefixDirName) {
					prefixDirName = prefixDirName[1:]
				}
				absolutePrefixPath := filepath.Join(testRootDir, prefixDirName)
				createdPrefixDir = absolutePrefixPath
				currentTestPrefixPath = absolutePrefixPath

				if err := os.MkdirAll(absolutePrefixPath, 0755); err != nil {
					t.Fatalf("Failed to create PATH_PREFIX directory %s: %v", absolutePrefixPath, err)
				}

				err := os.WriteFile(filepath.Join(absolutePrefixPath, "file1.txt"), []byte("content1_in_prefix"), 0644)
				if err != nil {
					t.Fatalf("Failed to create file1.txt in %s: %v", absolutePrefixPath, err)
				}

				dir1InPrefix := filepath.Join(absolutePrefixPath, "dir1")
				if err := os.MkdirAll(dir1InPrefix, 0755); err != nil {
					t.Fatalf("Failed to create dir1 in %s: %v", absolutePrefixPath, err)
				}

				err = os.WriteFile(filepath.Join(dir1InPrefix, "file2.txt"), []byte("content2_in_prefix_dir1"), 0644)
				if err != nil {
					t.Fatalf("Failed to create file2.txt in %s: %v", dir1InPrefix, err)
				}

				emptyDirInPrefix := filepath.Join(absolutePrefixPath, "empty_dir")
				if err := os.MkdirAll(emptyDirInPrefix, 0755); err != nil {
					t.Fatalf("Failed to create empty_dir in %s: %v", absolutePrefixPath, err)
				}
			}
			if err := os.Setenv("PATH_PREFIX", currentTestPrefixPath); err != nil {
				t.Fatalf("Failed to set PATH_PREFIX: %v", err)
			}
			defer func() {
				if err := os.Unsetenv("PATH_PREFIX"); err != nil {
					t.Logf("Failed to unset PATH_PREFIX: %v", err)
				}
				if createdPrefixDir != "" {
					if err := os.RemoveAll(createdPrefixDir); err != nil {
						t.Logf("Failed to clean up prefix directory %s: %v", createdPrefixDir, err)
					}
				}
			}()

			requestURL := "/list"
			if tt.queryD != "" {
				requestURL = fmt.Sprintf("/list?d=%s", tt.queryD)
			}

			req := httptest.NewRequest(http.MethodGet, requestURL, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			if assert.NoError(t, ListDirectoryHandler(c)) {
				assert.Equal(t, tt.expectedStatus, rec.Code)
				if tt.expectedStatus == http.StatusOK {
					var response DirectoryResponse
					if assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response)) {
						assert.Equal(t, tt.expectedPath, response.Path)

						assert.Len(t, response.Entries, len(tt.expectedEntryNames))

						entryNames := make([]string, len(response.Entries))
						entryTypes := make([]string, len(response.Entries))
						for i, entry := range response.Entries {
							entryNames[i] = entry.Name
							entryTypes[i] = entry.Type
						}

						for _, expectedName := range tt.expectedEntryNames {
							assert.Contains(t, entryNames, expectedName)
						}

						for _, expectedType := range tt.expectedEntryTypes {
							assert.Contains(t, entryTypes, expectedType)
						}

						if tt.expectParentLink {
							assert.NotNil(t, response.ParentLink)
							if response.ParentLink != nil {
								assert.Equal(t, tt.expectedParentLink, *response.ParentLink)
							}
						} else {
							assert.Nil(t, response.ParentLink)
						}
					}
				}
			}
		})
	}
}

func TestListDirectoryHandler_PathPrefixValidation(t *testing.T) {
	originalWd, _ := os.Getwd()
	testRootDir, cleanup := setupTestEnvironment(t)
	defer cleanup()
	err := os.Chdir(testRootDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Logf("Failed to change directory back to originalWd: %v", err)
		}
	}()

	e := echo.New()

	tests := []struct {
		name             string
		queryD           string
		pathPrefixEnv    string
		expectedStatus   int
		expectedErrorMsg string
	}{
		{
			name:           "Allowed path with prefix (d=dir1, prefix=/serve)",
			queryD:         "dir1",
			pathPrefixEnv:  "/serve",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Prefix is /serve and path is not allowed (d=/, prefix=/serve)",
			pathPrefixEnv:  "/serve",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Prefix is / and path is allowed (d=dir1, prefix=/) ",
			queryD:         "dir1",
			pathPrefixEnv:  "/",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Prefix is /app, path is /app (d is empty, prefix=/app)",
			pathPrefixEnv:  "/app",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Prefix is /app, path is / (d=/, prefix=/app)",
			queryD:         "/",
			pathPrefixEnv:  "/app",
			expectedStatus: http.StatusOK,
		},
		{
			name:             "Attempt to access parent of prefix (d=.., prefix=/app/sub)",
			queryD:           "..",
			pathPrefixEnv:    "/app/sub",
			expectedStatus:   http.StatusForbidden,
			expectedErrorMsg: "access to the requested path is forbidden (path traversal attempt?)",
		},
		{
			name:             "Attempt to access parent of prefix leading outside (d=../../, prefix=/app)",
			queryD:           "../..",
			pathPrefixEnv:    "/app",
			expectedStatus:   http.StatusForbidden,
			expectedErrorMsg: "access to the requested path is forbidden (path traversal attempt?)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentTestPrefixPath := tt.pathPrefixEnv
			var createdPrefixDir string
			if tt.pathPrefixEnv != "" && tt.pathPrefixEnv != "/" {
				prefixDirName := tt.pathPrefixEnv
				if filepath.IsAbs(prefixDirName) {
					prefixDirName = prefixDirName[1:]
				}
				absolutePrefixPath := filepath.Join(testRootDir, prefixDirName)
				currentTestPrefixPath = absolutePrefixPath
				createdPrefixDir = absolutePrefixPath

				if err := os.MkdirAll(absolutePrefixPath, 0755); err != nil {
					t.Fatalf("Failed to create PATH_PREFIX directory %s: %v", absolutePrefixPath, err)
				}

				err := os.WriteFile(filepath.Join(absolutePrefixPath, "file1.txt"), []byte("content1_in_prefix_validation"), 0644)
				if err != nil {
					t.Fatalf("Failed to create file1.txt in %s for validation: %v", absolutePrefixPath, err)
				}

				dir1InPrefix := filepath.Join(absolutePrefixPath, "dir1")
				if err := os.MkdirAll(dir1InPrefix, 0755); err != nil {
					t.Fatalf("Failed to create dir1 in %s for validation: %v", absolutePrefixPath, err)
				}

				err = os.WriteFile(filepath.Join(dir1InPrefix, "file2.txt"), []byte("content2_in_prefix_dir1_validation"), 0644)
				if err != nil {
					t.Fatalf("Failed to create file2.txt in %s for validation: %v", dir1InPrefix, err)
				}

				emptyDirInPrefix := filepath.Join(absolutePrefixPath, "empty_dir")
				if err := os.MkdirAll(emptyDirInPrefix, 0755); err != nil {
					t.Fatalf("Failed to create empty_dir in %s for validation: %v", absolutePrefixPath, err)
				}
			}
			if err := os.Setenv("PATH_PREFIX", currentTestPrefixPath); err != nil {
				t.Fatalf("Failed to set PATH_PREFIX: %v", err)
			}
			defer func() {
				if err := os.Unsetenv("PATH_PREFIX"); err != nil {
					t.Logf("Failed to unset PATH_PREFIX: %v", err)
				}
				if createdPrefixDir != "" {
					if err := os.RemoveAll(createdPrefixDir); err != nil {
						t.Logf("Failed to clean up prefix directory %s: %v", createdPrefixDir, err)
					}
				}
			}()

			requestURL := "/list"
			if tt.queryD != "" {
				requestURL = fmt.Sprintf("/list?d=%s", tt.queryD)
			}
			req := httptest.NewRequest(http.MethodGet, requestURL, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := ListDirectoryHandler(c)

			assert.Equal(t, tt.expectedStatus, rec.Code)

			if tt.expectedStatus == http.StatusOK {
				assert.NoError(t, err)
			} else {
				if tt.expectedErrorMsg != "" {
					var errResp map[string]string
					jsonErr := json.Unmarshal(rec.Body.Bytes(), &errResp)
					if assert.NoError(t, jsonErr, "Failed to unmarshal error response JSON: %s", rec.Body.String()) {
						assert.Equal(t, tt.expectedErrorMsg, errResp["error"], "Error message mismatch")
					}
				}
			}
		})
	}
}

func TestListDirectoryHandler_ErrorCases(t *testing.T) {
	originalWd, _ := os.Getwd()
	testRootDir, cleanup := setupTestEnvironment(t)
	defer cleanup()
	err := os.Chdir(testRootDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Logf("Failed to change directory back to originalWd: %v", err)
		}
	}()

	e := echo.New()

	tests := []struct {
		name           string
		queryD         string
		pathPrefixEnv  string
		expectedStatus int
	}{
		{
			name:           "Directory not found (d=non_existent_dir)",
			queryD:         "non_existent_dir",
			pathPrefixEnv:  "",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "Directory not found with non-existent prefix",
			queryD:         "non_existent_dir_in_prefix",
			pathPrefixEnv:  "/non_existent_prefix",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.Setenv("PATH_PREFIX", tt.pathPrefixEnv); err != nil {
				t.Fatalf("Failed to set PATH_PREFIX: %v", err)
			}
			defer func() {
				if err := os.Unsetenv("PATH_PREFIX"); err != nil {
					t.Logf("Failed to unset PATH_PREFIX: %v", err)
				}
			}()

			requestURL := "/list"
			if tt.queryD != "" {
				requestURL = fmt.Sprintf("/list?d=%s", tt.queryD)
			}
			req := httptest.NewRequest(http.MethodGet, requestURL, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := ListDirectoryHandler(c)

			var currentExpectedErrorMsg string
			switch tt.name {
			case "Directory not found (d=non_existent_dir)":
				currentExpectedErrorMsg = fmt.Sprintf("Directory not found: /%s", tt.queryD)
			case "Directory not found with non-existent prefix":
				currentExpectedErrorMsg = fmt.Sprintf("PATH_PREFIX %s not found", tt.pathPrefixEnv)
			default:
			}

			if tt.expectedStatus == http.StatusOK {
				assert.NoError(t, err, "Expected no error for status OK but got one")
				assert.Equal(t, tt.expectedStatus, rec.Code)
			} else {
				assert.Equal(t, tt.expectedStatus, rec.Code, "Recorder HTTP status code mismatch")
				if currentExpectedErrorMsg != "" {
					var respJSON map[string]string
					jsonErr := json.Unmarshal(rec.Body.Bytes(), &respJSON)
					assert.NoError(t, jsonErr, "Failed to unmarshal error response JSON")
					assert.Equal(t, currentExpectedErrorMsg, respJSON["error"], "Error message mismatch in JSON body")
				}
			}
		})
	}
}
