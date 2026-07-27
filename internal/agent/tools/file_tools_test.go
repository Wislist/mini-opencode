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

func TestReadToolReadsLineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(FileOptions{WorkDir: dir})
	out, err := tool.Run(context.Background(), fileToolInput(ReadToolName, map[string]any{
		"path":   "sample.txt",
		"offset": 2,
		"limit":  1,
	}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "2| two") {
		t.Fatalf("content = %q", out.Content)
	}
}

func TestWriteToolWritesFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteTool(FileOptions{WorkDir: dir})

	out, err := tool.Run(context.Background(), fileToolInput(WriteToolName, map[string]any{
		"path":    "created.txt",
		"content": "hello",
	}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Metadata["created"] != true {
		t.Fatalf("created = %v", out.Metadata["created"])
	}
	data, err := os.ReadFile(filepath.Join(dir, "created.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("file content = %q", data)
	}
}

func TestEditToolReplacesExactString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(FileOptions{WorkDir: dir})
	_, err := tool.Run(context.Background(), fileToolInput(EditToolName, map[string]any{
		"path":       "sample.txt",
		"old_string": "world",
		"new_string": "agent",
	}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello agent" {
		t.Fatalf("file content = %q", data)
	}
}

func TestEditToolRejectsMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("x x"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditTool(FileOptions{WorkDir: dir})
	_, err := tool.Run(context.Background(), fileToolInput(EditToolName, map[string]any{
		"path":       "sample.txt",
		"old_string": "x",
		"new_string": "y",
	}))
	if err == nil {
		t.Fatal("expected multiple match error")
	}
}

func TestLSToolListsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewLSTool(FileOptions{WorkDir: dir})
	out, err := tool.Run(context.Background(), fileToolInput(LSToolName, map[string]any{"path": "."}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "folder") || !strings.Contains(out.Content, "file.txt") {
		t.Fatalf("content = %q", out.Content)
	}
}

func TestFileToolsRejectWorkspaceEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadTool(FileOptions{WorkDir: dir})

	_, err := tool.Run(context.Background(), fileToolInput(ReadToolName, map[string]any{"path": "../outside.txt"}))
	if err == nil {
		t.Fatal("expected workspace escape error")
	}
}

func fileToolInput(name string, args map[string]any) agent.ToolInput {
	data, _ := json.Marshal(args)
	return agent.ToolInput{
		CallID:    "call-1",
		Name:      name,
		Arguments: data,
	}
}
