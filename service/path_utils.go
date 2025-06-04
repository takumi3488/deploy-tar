package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveAndValidatePath resolves and validates the given path components.
// rawQuerySubDir: The subdirectory requested by the user.
// pathPrefixEnv: The environment variable specifying a path prefix.
// Returns:
//   targetDir: The absolute, validated path on the filesystem.
//   displayPath: The path to be displayed to the user.
//   err: An error if validation fails.
func ResolveAndValidatePath(rawQuerySubDir string, pathPrefixEnv string) (targetDir string, displayPath string, err error) {
	// Clean pathPrefixEnv
	cleanedPathPrefix := filepath.Clean(pathPrefixEnv)
	if cleanedPathPrefix == "." || cleanedPathPrefix == "/" {
		cleanedPathPrefix = ""
	}

	effectiveQuerySubDir := rawQuerySubDir
	if cleanedPathPrefix != "" {
		if rawQuerySubDir == "/" {
			effectiveQuerySubDir = ""
		} else if filepath.IsAbs(rawQuerySubDir) && strings.HasPrefix(rawQuerySubDir, cleanedPathPrefix) {
			// Make effectiveQuerySubDir relative to the prefix
			relPath, errRel := filepath.Rel(cleanedPathPrefix, rawQuerySubDir)
			if errRel == nil {
				effectiveQuerySubDir = relPath
			}
			// If errRel != nil, effectiveQuerySubDir remains rawQuerySubDir.
			// This should be a rare case, possibly caught by later checks or an error itself.
		}
	}

	// Preliminary Traversal Check (copied from handler/list.go)
	prelimCleanedRawQuerySubDir := filepath.Clean(rawQuerySubDir) // Based on original rawQuerySubDir for this check
	if cleanedPathPrefix != "" {
		if strings.HasPrefix(prelimCleanedRawQuerySubDir, "..") {
			return "", "", errors.New("Access to the requested path is forbidden (path traversal attempt?)")
		}
		if filepath.IsAbs(rawQuerySubDir) && !strings.HasPrefix(rawQuerySubDir, cleanedPathPrefix) && rawQuerySubDir != "/" {
			return "", "", errors.New("Access to the requested path is forbidden (path traversal attempt?)")
		}
	}

	// Calculate targetFsPath (for file system access)
	targetFsPath := filepath.Clean(effectiveQuerySubDir)
	if targetFsPath == "" || targetFsPath == "." || targetFsPath == "/" {
		targetFsPath = "."
	}

	// Determine baseDirForAccess
	baseDirForAccess := "."
	if cleanedPathPrefix != "" {
		info, err := os.Stat(cleanedPathPrefix)
		if err != nil {
			if os.IsNotExist(err) {
				return "", "", fmt.Errorf("PATH_PREFIX %s not found", cleanedPathPrefix)
			}
			return "", "", fmt.Errorf("Error accessing PATH_PREFIX %s: %w", cleanedPathPrefix, err)
		}
		if !info.IsDir() {
			return "", "", fmt.Errorf("PATH_PREFIX %s is not a directory", cleanedPathPrefix)
		}
		baseDirForAccess = cleanedPathPrefix
	}

	// Calculate targetDir
	resolvedTargetDir := filepath.Join(baseDirForAccess, targetFsPath)
	resolvedTargetDir = filepath.Clean(resolvedTargetDir)

	// Full Path Validation (Security Checks)
	absTargetDir, err := filepath.Abs(resolvedTargetDir)
	if err != nil {
		return "", "", fmt.Errorf("Error getting absolute path for target: %w", err)
	}

	if cleanedPathPrefix != "" {
		absCleanedPathPrefix, err := filepath.Abs(cleanedPathPrefix)
		if err != nil {
			return "", "", fmt.Errorf("Error getting absolute path for PATH_PREFIX: %w", err)
		}
		relPath, err := filepath.Rel(absCleanedPathPrefix, absTargetDir)
		if err != nil {
			return "", "", fmt.Errorf("Error calculating relative path: %w", err)
		}
		if strings.HasPrefix(relPath, "..") || relPath == ".." {
			return "", "", errors.New("Access to the requested path is forbidden (resolved path outside prefix)")
		}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("Error getting current working directory: %w", err)
		}
		absCwd, err := filepath.Abs(cwd)
		if err != nil {
			return "", "", fmt.Errorf("Error getting absolute path for current working directory: %w", err)
		}
		relPath, err := filepath.Rel(absCwd, absTargetDir)
		if err != nil {
			return "", "", fmt.Errorf("Error calculating relative path from CWD: %w", err)
		}
		if strings.HasPrefix(relPath, "..") || relPath == ".." {
			return "", "", errors.New("Access to the requested path is forbidden (resolved path outside CWD)")
		}
	}

	// Calculate displayPath
	tempDisplayPath := rawQuerySubDir
	if cleanedPathPrefix != "" {
		if rawQuerySubDir == "/" {
			tempDisplayPath = ""
		} else if filepath.IsAbs(rawQuerySubDir) && strings.HasPrefix(rawQuerySubDir, cleanedPathPrefix) {
			rel, errRel := filepath.Rel(cleanedPathPrefix, rawQuerySubDir)
			if errRel == nil { // Should be true if HasPrefix was true
				tempDisplayPath = rel
			}
			// If error, tempDisplayPath remains rawQuerySubDir, normalization will handle it.
		}
	}

	// Normalize tempDisplayPath for display
	if tempDisplayPath == "" || tempDisplayPath == "." {
		displayPath = "/"
	} else {
		cleanedDisplayPath := filepath.Clean(tempDisplayPath)
		if cleanedDisplayPath == "." { // Possible if tempDisplayPath was "." or ".\", etc.
			displayPath = "/"
		} else {
			// Ensure it starts with a slash for non-root paths
			if !strings.HasPrefix(cleanedDisplayPath, "/") {
				displayPath = "/" + cleanedDisplayPath
			} else {
				displayPath = cleanedDisplayPath
			}
		}
	}
	// Ensure displayPath doesn't end with a slash unless it's the root path "/"
	if len(displayPath) > 1 && strings.HasSuffix(displayPath, "/") {
		displayPath = strings.TrimRight(displayPath, "/")
	}

	return absTargetDir, displayPath, nil
}
