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

// setupTestEnvironment creates a directory and files for testing, and returns its path.
// It also returns a cleanup function.
func setupTestEnvironment(t *testing.T) (string, func()) {
	t.Helper()
	// Create a temporary root directory
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
		// Attempt to change back to originalWd, though it might also fail
		if errChdir := os.Chdir(originalWd); errChdir != nil {
			t.Logf("Failed to change directory back to originalWd during setup cleanup: %v", errChdir)
		}
		t.Fatalf("Failed to change current working directory to %s: %v", rootDir, err)
	}

	// Create test file and directory structure
	// rootDir/ (which is now current working directory for the test)
	//   |- file1.txt
	//   |- dir1/
	//   |  |- file2.txt
	//   |- empty_dir/

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
			// Log or handle error, but don't fail the test here as it's cleanup
			// Using t.Logf or fmt.Fprintf(os.Stderr, ...)
			t.Logf("Warning: failed to change directory back to %s: %v", originalWd, errChdirBack)
		}
		errRemoveAll := os.RemoveAll(rootDir)
		if errRemoveAll != nil {
			t.Logf("Warning: failed to remove temp dir %s: %v", rootDir, errRemoveAll)
		}
	}
	// rootDir is returned but its significance is reduced as CWD is changed.
	// Callers might not need it if they operate on CWD.
	return rootDir, cleanup
}

func TestListDirectoryHandler_SuccessCases(t *testing.T) {
	originalWd, _ := os.Getwd() // Save the original working directory
	testRootDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Since the handler is based on the current working directory,
	// move to the test root directory
	err := os.Chdir(testRootDir)
	if err != nil {
		t.Fatalf("Failed to change directory to test root: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Logf("Failed to change directory back to originalWd: %v", err)
		}
	}() // Return to the original directory after the test

	e := echo.New()

	tests := []struct {
		name               string
		queryD             string // Value of query parameter "d"
		pathPrefixEnv      string // Environment variable PATH_PREFIX
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
			expectedParentLink: "/list",
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
			expectedParentLink: "/list",
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
			expectedParentLink: "/list",
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
			expectedParentLink: "/list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentTestPrefixPath := tt.pathPrefixEnv
			var createdPrefixDir string // Track the directory we create for cleanup
			if tt.pathPrefixEnv != "" && tt.pathPrefixEnv != "/" {
				// If tt.pathPrefixEnv is in a format like "/serve", extract "serve"
				prefixDirName := tt.pathPrefixEnv
				if filepath.IsAbs(prefixDirName) {
					prefixDirName = prefixDirName[1:]
				}
				// Combine with testRootDir (current working directory) to make it an absolute path
				absolutePrefixPath := filepath.Join(testRootDir, prefixDirName)
				currentTestPrefixPath = absolutePrefixPath // Update the path to be set in the environment variable
				createdPrefixDir = absolutePrefixPath      // Store for cleanup

				if err := os.MkdirAll(absolutePrefixPath, 0755); err != nil {
					t.Fatalf("Failed to create PATH_PREFIX directory %s: %v", absolutePrefixPath, err)
				}

				// Populate this prefix directory
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
				// Clean up the created prefix directory
				if createdPrefixDir != "" {
					if err := os.RemoveAll(createdPrefixDir); err != nil {
						t.Logf("Failed to clean up prefix directory %s: %v", createdPrefixDir, err)
					}
				}
			}()

			// Construct request URL
			requestURL := "/list"
			if tt.queryD != "" {
				requestURL = fmt.Sprintf("/list?d=%s", tt.queryD)
			}

			req := httptest.NewRequest(http.MethodGet, requestURL, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			// c.Param("*") is no longer used, so SetParamNames and SetParamValues are not needed

			if assert.NoError(t, ListDirectoryHandler(c)) {
				assert.Equal(t, tt.expectedStatus, rec.Code)
				if tt.expectedStatus == http.StatusOK {
					// Parse JSON response
					var response DirectoryResponse
					if assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response)) {
						// Check path
						assert.Equal(t, tt.expectedPath, response.Path)

						// Check entries count
						assert.Len(t, response.Entries, len(tt.expectedEntryNames))

						// Check each entry
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

						// Check parent link
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
		queryD           string // Value of query parameter "d"
		pathPrefixEnv    string
		expectedStatus   int
		expectedErrorMsg string // Part of the expected message in case of error
	}{
		{
			name:           "Allowed path with prefix (d=dir1, prefix=/serve)",
			queryD:         "dir1",
			pathPrefixEnv:  "/serve",
			expectedStatus: http.StatusOK,
		},
		{
			name:             "Forbidden path (d=../outside, prefix=/serve)", // path traversal attempt
			queryD:           "../outside",                                   // Attempt to go outside pathPrefix
			pathPrefixEnv:    "/serve",
			expectedStatus:   http.StatusForbidden,
			expectedErrorMsg: "Access to the requested path is forbidden (path traversal attempt?)",
		},
		{
			name:           "Prefix is / and path is allowed (d=dir1, prefix=/) ",
			queryD:         "dir1",
			pathPrefixEnv:  "/",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Prefix is /app, path is /app (d is empty, prefix=/app)",
			queryD:         "", // Root of pathPrefix
			pathPrefixEnv:  "/app",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Prefix is /app, query d is / (serves prefix root)", // d=/ points to the root of PATH_PREFIX
			queryD:         "/",
			pathPrefixEnv:  "/app",
			expectedStatus: http.StatusOK,
			// expectedErrorMsg: "Access to the requested path is forbidden (path traversal attempt?)", // No error expected
		},
		{
			name:             "Attempt to access parent of prefix (d=../, prefix=/app/sub)",
			queryD:           "..",
			pathPrefixEnv:    "/app/sub", // Attempt to access /app
			expectedStatus:   http.StatusForbidden,
			expectedErrorMsg: "Access to the requested path is forbidden (path traversal attempt?)",
		},
		{
			name:             "Attempt to access parent of prefix leading outside (d=../../, prefix=/app)",
			queryD:           "../..", // Attempt to go outside /app
			pathPrefixEnv:    "/app",
			expectedStatus:   http.StatusForbidden,
			expectedErrorMsg: "Access to the requested path is forbidden (path traversal attempt?)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentTestPrefixPath := tt.pathPrefixEnv
			var createdPrefixDir string // Track the directory we create for cleanup
			if tt.pathPrefixEnv != "" && tt.pathPrefixEnv != "/" {
				prefixDirName := tt.pathPrefixEnv
				if filepath.IsAbs(prefixDirName) {
					prefixDirName = prefixDirName[1:]
				}
				absolutePrefixPath := filepath.Join(testRootDir, prefixDirName)
				currentTestPrefixPath = absolutePrefixPath
				createdPrefixDir = absolutePrefixPath // Store for cleanup

				if err := os.MkdirAll(absolutePrefixPath, 0755); err != nil {
					t.Fatalf("Failed to create PATH_PREFIX directory %s: %v", absolutePrefixPath, err)
				}

				// Populate this prefix directory
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
				// Clean up the created prefix directory
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
			// c.Param is not used

			err := ListDirectoryHandler(c)

			if tt.expectedStatus == http.StatusOK {
				assert.NoError(t, err)
				assert.Equal(t, http.StatusOK, rec.Code)
			} else {
				// Expect an error to be returned (echo.HTTPError)
				// assert.Error(t, err) // This fails if nil
				if assert.NotNil(t, err, "Expected an error but got nil") {
					httpError, ok := err.(*echo.HTTPError)
					if assert.True(t, ok, "Expected error to be of type *echo.HTTPError") {
						assert.Equal(t, tt.expectedStatus, httpError.Code)
						if tt.expectedErrorMsg != "" {
							// httpError.Message can be an interface{}, so we need a type assertion
							if msgMap, ok := httpError.Message.(map[string]string); ok {
								assert.Equal(t, tt.expectedErrorMsg, msgMap["error"])
							} else {
								assert.Equal(t, tt.expectedErrorMsg, httpError.Message)
							}
						}
					}
				}
				// Also check the recorder's code (as the handler might call c.Error() without returning an error directly)
				// However, this handler returns errors directly, so checking err is primary
				assert.Equal(t, tt.expectedStatus, rec.Code)
				if rec.Code != http.StatusOK && tt.expectedErrorMsg != "" {
					assert.JSONEq(t, fmt.Sprintf(`{"error":"%s"}`, tt.expectedErrorMsg), rec.Body.String())
				}
			}
		})
	}
}

func TestListDirectoryHandler_ErrorCases(t *testing.T) {
	originalWd, _ := os.Getwd()
	testRootDir, cleanup := setupTestEnvironment(t) // testRootDir is not used, but for a clean environment
	defer cleanup()
	err := os.Chdir(testRootDir) // Access to non-existent directories is relative to the current directory
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
		expectedErrorMsg string // Expected path part in the error message
	}{
		{
			name:           "Directory not found (d=non_existent_dir)",
			queryD:         "non_existent_dir",
			pathPrefixEnv:  "",
			expectedStatus: http.StatusNotFound,
			// expectedErrorMsg is set dynamically in the test loop
		},
		{
			name:           "Directory not found with non-existent prefix",
			queryD:         "non_existent_dir_in_prefix",
			pathPrefixEnv:  "/this_prefix_definitely_should_not_exist_for_testing_purposes", // Use a highly unlikely path
			expectedStatus: http.StatusNotFound,
			// expectedErrorMsg is set dynamically in the test loop
		},
		// TODO: Permission denied case (requires setting up a directory with no read perms)
		// This test is difficult to set up in some environments, so skip or add a note for now
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
			// c.Param is not used

			err := ListDirectoryHandler(c)

			// Dynamically set expectedErrorMsg for these specific cases
			var currentExpectedErrorMsg string
			switch tt.name {
			case "Directory not found (d=non_existent_dir)":
				currentExpectedErrorMsg = fmt.Sprintf("Directory not found: %s", tt.queryD)
			case "Directory not found with non-existent prefix":
				// In this test case, the environment variable PATH_PREFIX is intended to be set to a non-existent absolute path
				// that is not relative to testRootDir, like /this_prefix_definitely_should_not_exist_for_testing_purposes.
				// Therefore, the error message should match the actual message returned by the handler.
				currentExpectedErrorMsg = fmt.Sprintf("Base directory specified by PATH_PREFIX not found: %s", tt.pathPrefixEnv)
			default:
				currentExpectedErrorMsg = tt.expectedErrorMsg // Use pre-defined if any for other tests
			}

			// err is already declared and assigned at line 396
			// err := ListDirectoryHandler(c) // Call the handler  <- This line is the duplicate causing the error

			if tt.expectedStatus == http.StatusOK {
				if assert.NoError(t, err, "Expected no error for status OK but got one") {
					assert.Equal(t, tt.expectedStatus, rec.Code)
				}
			} else { // Expecting an error status (e.g., 403, 404)
				if assert.NotNil(t, err, "Expected an error but got nil for status %d", tt.expectedStatus) {
					httpError, ok := err.(*echo.HTTPError)
					if assert.True(t, ok, "Expected error to be of type *echo.HTTPError") {
						assert.Equal(t, tt.expectedStatus, httpError.Code, "HTTP status code mismatch")
						if currentExpectedErrorMsg != "" { // Only check message if one is expected
							if msgMap, ok := httpError.Message.(map[string]string); ok {
								assert.Equal(t, currentExpectedErrorMsg, msgMap["error"], "Error message mismatch")
							} else {
								assert.Equal(t, currentExpectedErrorMsg, httpError.Message, "Error message mismatch")
							}
						}
					}
				}
				// Also check recorder status, as handler might use c.Error() which sets recorder status
				assert.Equal(t, tt.expectedStatus, rec.Code, "Recorder HTTP status code mismatch")
				if rec.Code != http.StatusOK && currentExpectedErrorMsg != "" {
					// Check the body of the response if an error message is expected
					assert.JSONEq(t, fmt.Sprintf(`{"error":"%s"}`, currentExpectedErrorMsg), rec.Body.String(), "Response body JSON mismatch")
				}
			}
		})
	}
}
