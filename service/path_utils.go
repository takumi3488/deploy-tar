package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolveAndValidatePath(rawQuerySubDir string, pathPrefixEnv string) (targetDir string, displayPath string, err error) {
	cleanedPathPrefix := filepath.Clean(pathPrefixEnv)
	if cleanedPathPrefix == "." || cleanedPathPrefix == "/" {
		cleanedPathPrefix = ""
	}

	effectiveQuerySubDir := rawQuerySubDir
	if cleanedPathPrefix != "" {
		if rawQuerySubDir == "/" {
			effectiveQuerySubDir = ""
		} else if filepath.IsAbs(rawQuerySubDir) && strings.HasPrefix(rawQuerySubDir, cleanedPathPrefix) {
			relPath, errRel := filepath.Rel(cleanedPathPrefix, rawQuerySubDir)
			if errRel == nil {
				effectiveQuerySubDir = relPath
			}
		}
	}

	prelimCleanedRawQuerySubDir := filepath.Clean(rawQuerySubDir)
	if cleanedPathPrefix != "" {
		if strings.HasPrefix(prelimCleanedRawQuerySubDir, "..") {
			return "", "", errors.New("access to the requested path is forbidden (path traversal attempt?)")
		}
		if filepath.IsAbs(rawQuerySubDir) && !strings.HasPrefix(rawQuerySubDir, cleanedPathPrefix) && rawQuerySubDir != "/" {
			return "", "", errors.New("access to the requested path is forbidden (path traversal attempt?)")
		}
	}

	targetFsPath := filepath.Clean(effectiveQuerySubDir)
	if targetFsPath == "" || targetFsPath == "." || targetFsPath == "/" {
		targetFsPath = "."
	}

	baseDirForAccess := "."
	if cleanedPathPrefix != "" {
		info, err := os.Stat(cleanedPathPrefix)
		if err != nil {
			if os.IsNotExist(err) {
				return "", "", fmt.Errorf("PATH_PREFIX %s not found", cleanedPathPrefix)
			}
			return "", "", fmt.Errorf("error accessing PATH_PREFIX %s: %w", cleanedPathPrefix, err)
		}
		if !info.IsDir() {
			return "", "", fmt.Errorf("PATH_PREFIX %s is not a directory", cleanedPathPrefix)
		}
		baseDirForAccess = cleanedPathPrefix
	}

	resolvedTargetDir := filepath.Join(baseDirForAccess, targetFsPath)
	resolvedTargetDir = filepath.Clean(resolvedTargetDir)

	absTargetDir, err := filepath.Abs(resolvedTargetDir)
	if err != nil {
		return "", "", fmt.Errorf("error getting absolute path for target: %w", err)
	}

	if cleanedPathPrefix != "" {
		absCleanedPathPrefix, err := filepath.Abs(cleanedPathPrefix)
		if err != nil {
			return "", "", fmt.Errorf("error getting absolute path for PATH_PREFIX: %w", err)
		}
		relPath, err := filepath.Rel(absCleanedPathPrefix, absTargetDir)
		if err != nil {
			return "", "", fmt.Errorf("error calculating relative path: %w", err)
		}
		if strings.HasPrefix(relPath, "..") || relPath == ".." {
			return "", "", errors.New("access to the requested path is forbidden (resolved path outside prefix)")
		}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("error getting current working directory: %w", err)
		}
		absCwd, err := filepath.Abs(cwd)
		if err != nil {
			return "", "", fmt.Errorf("error getting absolute path for current working directory: %w", err)
		}
		relPath, err := filepath.Rel(absCwd, absTargetDir)
		if err != nil {
			return "", "", fmt.Errorf("error calculating relative path from CWD: %w", err)
		}
		if strings.HasPrefix(relPath, "..") || relPath == ".." {
			return "", "", errors.New("access to the requested path is forbidden (resolved path outside CWD)")
		}
	}

	tempDisplayPath := rawQuerySubDir
	if cleanedPathPrefix != "" {
		if rawQuerySubDir == "/" {
			tempDisplayPath = ""
		} else if filepath.IsAbs(rawQuerySubDir) && strings.HasPrefix(rawQuerySubDir, cleanedPathPrefix) {
			rel, errRel := filepath.Rel(cleanedPathPrefix, rawQuerySubDir)
			if errRel == nil {
				tempDisplayPath = rel
			}
		}
	}

	if tempDisplayPath == "" || tempDisplayPath == "." {
		displayPath = "/"
	} else {
		cleanedDisplayPath := filepath.Clean(tempDisplayPath)
		if cleanedDisplayPath == "." {
			displayPath = "/"
		} else {
			if !strings.HasPrefix(cleanedDisplayPath, "/") {
				displayPath = "/" + cleanedDisplayPath
			} else {
				displayPath = cleanedDisplayPath
			}
		}
	}
	if len(displayPath) > 1 && strings.HasSuffix(displayPath, "/") {
		displayPath = strings.TrimRight(displayPath, "/")
	}

	return absTargetDir, displayPath, nil
}
