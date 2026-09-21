package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

// fakeObserver is an in-memory FileObserver for tool tests.
type fakeObserver struct {
	read      map[string]bool
	snapshots map[string][]byte
}

func newFakeObserver() *fakeObserver {
	return &fakeObserver{read: map[string]bool{}, snapshots: map[string][]byte{}}
}

func (o *fakeObserver) RecordRead(path string) error {
	o.read[path] = true
	return nil
}

func (o *fakeObserver) HasRead(path string) bool { return o.read[path] }

func (o *fakeObserver) Snapshot(path string, content []byte) error {
	o.snapshots[path] = append([]byte(nil), content...)
	return nil
}

func observerOptions(workDir string, observer FileObserver, require bool) FileOptions {
	return FileOptions{
		WorkDir:                workDir,
		Observer:               observer,
		RequireReadBeforeWrite: require,
	}
}

func runTool(t *testing.T, tool agent.Tool, args map[string]any) (agent.ToolOutput, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Run(context.Background(), agent.ToolInput{CallID: "c1", Name: tool.Definition().Name, Arguments: raw})
}

func TestWriteRequiresReadBeforeOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(target, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	observer := newFakeObserver()
	options := observerOptions(dir, observer, true)

	write := NewWriteTool(options)
	if _, err := runTool(t, write, map[string]any{"path": "a.txt", "content": "clobbered"}); err == nil {
		t.Fatal("write without a prior read succeeded, want error")
	} else if !strings.Contains(err.Error(), "read before write") {
		t.Fatalf("error = %v, want read-before-write message", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "original" {
		t.Fatalf("file was modified despite the block: %q", data)
	}

	// Reading the file unlocks the overwrite and records a snapshot.
	read := NewReadTool(options)
	if _, err := runTool(t, read, map[string]any{"path": "a.txt"}); err != nil {
		t.Fatalf("read error = %v", err)
	}
	if _, err := runTool(t, write, map[string]any{"path": "a.txt", "content": "clobbered"}); err != nil {
		t.Fatalf("write after read error = %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "clobbered" {
		t.Fatalf("content = %q", data)
	}
	if got := string(observer.snapshots[target]); got != "original" {
		t.Fatalf("snapshot = %q, want the pre-modification content", got)
	}
}

func TestWriteAllowsNewFileWithoutRead(t *testing.T) {
	dir := t.TempDir()
	observer := newFakeObserver()
	write := NewWriteTool(observerOptions(dir, observer, true))

	out, err := runTool(t, write, map[string]any{"path": "brand-new.txt", "content": "hello"})
	if err != nil {
		t.Fatalf("write new file error = %v", err)
	}
	if created, _ := out.Metadata["created"].(bool); !created {
		t.Fatalf("metadata = %#v", out.Metadata)
	}
	if _, ok := observer.snapshots[filepath.Join(dir, "brand-new.txt")]; ok {
		t.Fatal("a new file must not produce a snapshot")
	}
}

func TestEditRequiresReadBeforeOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(target, []byte("alpha beta"), 0644); err != nil {
		t.Fatal(err)
	}
	observer := newFakeObserver()
	options := observerOptions(dir, observer, true)

	edit := NewEditTool(options)
	if _, err := runTool(t, edit, map[string]any{"path": "b.txt", "old_string": "alpha", "new_string": "gamma"}); err == nil {
		t.Fatal("edit without a prior read succeeded, want error")
	}

	read := NewReadTool(options)
	if _, err := runTool(t, read, map[string]any{"path": "b.txt"}); err != nil {
		t.Fatalf("read error = %v", err)
	}
	out, err := runTool(t, edit, map[string]any{"path": "b.txt", "old_string": "alpha", "new_string": "gamma"})
	if err != nil {
		t.Fatalf("edit after read error = %v", err)
	}
	if snapshot, _ := out.Metadata["snapshot"].(bool); !snapshot {
		t.Fatalf("metadata = %#v, want snapshot flag", out.Metadata)
	}
	if data, _ := os.ReadFile(target); string(data) != "gamma beta" {
		t.Fatalf("content = %q", data)
	}
	if got := string(observer.snapshots[target]); got != "alpha beta" {
		t.Fatalf("snapshot = %q", got)
	}
}

// Disabling the rule (or having no observer) keeps the previous behavior.
func TestReadBeforeWriteCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "c.txt")
	if err := os.WriteFile(target, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	write := NewWriteTool(observerOptions(dir, newFakeObserver(), false))
	if _, err := runTool(t, write, map[string]any{"path": "c.txt", "content": "new"}); err != nil {
		t.Fatalf("write with the rule disabled error = %v", err)
	}

	noObserver := NewWriteTool(FileOptions{WorkDir: dir, RequireReadBeforeWrite: true})
	if _, err := runTool(t, noObserver, map[string]any{"path": "c.txt", "content": "newer"}); err != nil {
		t.Fatalf("write without an observer error = %v", err)
	}
}

func TestTrailingBackgroundOperator(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{"sleep 5 &", true},
		{"sleep 5 & ", true},  // trailing whitespace used to slip through
		{"sleep 5 &\n", true}, // and a trailing newline too
		{"make && echo done", false},
		{"make &&", false}, // logical AND, not a background operator
		{"echo 'a & b'", false},
		{"echo hi > log 2>&1", false},
		{"echo hi", false},
	}
	for _, tc := range cases {
		if got := trailingBackgroundOperator(tc.command); got != tc.want {
			t.Fatalf("trailingBackgroundOperator(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestReadToolTruncatesByLineBudget(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "many.txt"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	read := NewReadTool(FileOptions{WorkDir: dir})
	out, err := runTool(t, read, map[string]any{"path": "many.txt", "limit": 5})
	if err != nil {
		t.Fatalf("read error = %v", err)
	}
	if out.Metadata["truncated_by"] != "lines" {
		t.Fatalf("metadata = %#v, want a line truncation", out.Metadata)
	}
	if !strings.Contains(out.Content, "continue with offset=6") {
		t.Fatalf("content missing continuation hint: %q", out.Content)
	}
	if got, _ := out.Metadata["total_lines"].(int); got != 51 {
		t.Fatalf("total_lines = %v, want 51", out.Metadata["total_lines"])
	}
}

func TestReadToolTruncatesByByteBudget(t *testing.T) {
	dir := t.TempDir()
	// One enormous line: a line budget alone would return megabytes.
	huge := strings.Repeat("x", 5000)
	if err := os.WriteFile(filepath.Join(dir, "min.js"), []byte(huge+"\n"+huge+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	read := NewReadTool(FileOptions{
		WorkDir:         dir,
		InstructionData: InstructionData{MaxOutputLength: 800},
	})
	out, err := runTool(t, read, map[string]any{"path": "min.js"})
	if err != nil {
		t.Fatalf("read error = %v", err)
	}
	if out.Metadata["truncated_by"] != "bytes" {
		t.Fatalf("metadata = %#v, want a byte truncation", out.Metadata)
	}
	if len(out.Content) > 800+200 {
		t.Fatalf("content = %d bytes, want it near the 800 byte budget", len(out.Content))
	}
}

func TestReadToolClampsLimit(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("x\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	read := NewReadTool(FileOptions{WorkDir: dir})
	out, err := runTool(t, read, map[string]any{"path": "big.txt", "limit": 100000})
	if err != nil {
		t.Fatalf("read error = %v", err)
	}
	if got, _ := out.Metadata["lines"].(int); got != MaxReadLimit {
		t.Fatalf("lines = %v, want the %d line cap", out.Metadata["lines"], MaxReadLimit)
	}
}
