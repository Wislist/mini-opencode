package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FileOptions struct {
	WorkDir         string
	AllowedRoots    []string
	InstructionData InstructionData
}

func normalizeFileOptions(options FileOptions) FileOptions {
	if options.WorkDir == "" {
		options.WorkDir, _ = os.Getwd()
	}
	options.WorkDir, _ = filepath.Abs(options.WorkDir)
	options.AllowedRoots = normalizeRoots(options.AllowedRoots)
	if options.InstructionData.MaxOutputLength == 0 {
		options.InstructionData = DefaultInstructionData()
	}
	return options
}

// normalizeRoots cleans and de-duplicates additional workspace roots.
func normalizeRoots(roots []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

func resolveWorkspacePath(workDir string, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	return resolveWorkspacePathRoots(workDir, nil, path)
}

// resolveWorkspacePathRoots resolves path against the working directory and
// any additional allowed roots. Relative paths resolve under the working
// directory; absolute paths are accepted when they fall inside the working
// directory or any allowed root. Returns an error when the path escapes all
// permitted roots.
func resolveWorkspacePathRoots(workDir string, allowedRoots []string, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(workDir, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	roots := append([]string{workDir}, allowedRoots...)
	for _, root := range roots {
		base, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, abs)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("path escapes workspace: %s", abs)
}

// resolveWorkspacePathWithOptions resolves a path using a FileOptions'
// working directory and allowed roots.
func resolveWorkspacePathWithOptions(options FileOptions, path string) (string, error) {
	return resolveWorkspacePathRoots(options.WorkDir, options.AllowedRoots, path)
}

func isLikelyBinary(data []byte) bool {
	limit := len(data)
	if limit > 8000 {
		limit = 8000
	}
	for _, b := range data[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}
