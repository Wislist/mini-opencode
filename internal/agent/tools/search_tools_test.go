package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

func TestGlobToolFindsMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"), "package main")
	mustWrite(t, filepath.Join(dir, "README.md"), "docs")

	tool := NewGlobTool(FileOptions{WorkDir: dir})
	out, err := tool.Run(context.Background(), searchToolInput(GlobToolName, map[string]any{"pattern": "*.go"}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "main.go") || strings.Contains(out.Content, "README.md") {
		t.Fatalf("content = %q", out.Content)
	}
}

func TestGrepToolFindsTextMatches(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.txt"), "alpha\nbeta")
	mustWrite(t, filepath.Join(dir, "b.txt"), "beta\ngamma")

	tool := NewGrepTool(FileOptions{WorkDir: dir})
	out, err := tool.Run(context.Background(), searchToolInput(GrepToolName, map[string]any{"query": "beta"}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "a.txt:2:beta") || !strings.Contains(out.Content, "b.txt:1:beta") {
		t.Fatalf("content = %q", out.Content)
	}
}

func TestGrepToolSkipsGitDirectory(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(gitDir, "config"), "secret")
	mustWrite(t, filepath.Join(dir, "visible.txt"), "secret")

	tool := NewGrepTool(FileOptions{WorkDir: dir})
	out, err := tool.Run(context.Background(), searchToolInput(GrepToolName, map[string]any{"query": "secret"}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Contains(out.Content, ".git") || !strings.Contains(out.Content, "visible.txt") {
		t.Fatalf("content = %q", out.Content)
	}
}

func TestSearchToolsRejectWorkspaceEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewGrepTool(FileOptions{WorkDir: dir})

	_, err := tool.Run(context.Background(), searchToolInput(GrepToolName, map[string]any{
		"query": "x",
		"path":  "../outside",
	}))
	if err == nil {
		t.Fatal("expected workspace escape error")
	}
}

func searchToolInput(name string, args map[string]any) agent.ToolInput {
	data, _ := json.Marshal(args)
	return agent.ToolInput{CallID: "call-1", Name: name, Arguments: data}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
