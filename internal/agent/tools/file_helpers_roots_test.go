package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWorkspacePathRootsAllowsAllowedRoot(t *testing.T) {
	workDir := t.TempDir()
	extra := t.TempDir()
	target := filepath.Join(extra, "file.txt")

	got, err := resolveWorkspacePathRoots(workDir, []string{extra}, target)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != target {
		t.Fatalf("got = %q, want %q", got, target)
	}
}

func TestResolveWorkspacePathRootsDeniesEscape(t *testing.T) {
	workDir := t.TempDir()
	extra := t.TempDir()

	_, err := resolveWorkspacePathRoots(workDir, []string{extra}, "/etc/hosts")
	if err == nil {
		t.Fatal("expected escape error, got nil")
	}
	if !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("err = %q", err)
	}
}

func TestResolveWorkspacePathWithOptionsUsesAllowedRoots(t *testing.T) {
	workDir := t.TempDir()
	extra := t.TempDir()
	target := filepath.Join(extra, "deep.go")

	options := FileOptions{WorkDir: workDir, AllowedRoots: []string{extra}}
	got, err := resolveWorkspacePathWithOptions(options, target)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != target {
		t.Fatalf("got = %q, want %q", got, target)
	}
}
