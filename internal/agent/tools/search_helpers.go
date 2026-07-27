package tools

import (
	"path/filepath"
	"strings"
)

var ignoredSearchDirs = map[string]bool{
	".git":         true,
	".gocache":     true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
}

func shouldSkipSearchPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if ignoredSearchDirs[part] {
			return true
		}
	}
	return false
}
