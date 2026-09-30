package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/session"
)

func TestRunBannerReportsTheVersion(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("/quit\n")

	if err := Run(context.Background(), in, &out); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Assert against the live version rather than a literal: the Makefile
	// injects a git-derived string, so a hardcoded value would fail depending
	// on whether the tree is tagged or dirty.
	got := out.String()
	if !strings.Contains(got, "mini-opencode "+version) {
		t.Fatalf("output missing version %q: %q", version, got)
	}
}

// TestSetVersionIgnoresEmpty documents that the linker-injected value is only
// applied when it carries something, so a plain `go build` keeps the fallback
// constant instead of reporting an empty version.
func TestSetVersionIgnoresEmpty(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	SetVersion("")
	if version != original {
		t.Fatalf("empty version replaced the fallback: %q", version)
	}
	SetVersion("   ")
	if version != original {
		t.Fatalf("blank version replaced the fallback: %q", version)
	}
	SetVersion("9.9.9")
	if version != "9.9.9" {
		t.Fatalf("version not overridden: %q", version)
	}
}

func TestConfirmToolAcceptsChineseAllow(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("允许\n"))
	var out bytes.Buffer
	confirm := confirmTool(scanner, &out, map[string]bool{}, t.TempDir())

	if !confirm(context.Background(), agent.ToolCall{Name: "bash"}, agent.ToolResult{}) {
		t.Fatal("expected confirmation to be accepted")
	}
}

func TestConfirmToolAlwaysAllowsForSession(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("a\n"))
	var out bytes.Buffer
	allowed := map[string]bool{}
	confirm := confirmTool(scanner, &out, allowed, t.TempDir())

	if !confirm(context.Background(), agent.ToolCall{Name: "edit"}, agent.ToolResult{}) {
		t.Fatal("expected the always answer to allow the call")
	}
	if !allowed["edit"] {
		t.Fatal("tool was not added to the session allowlist")
	}
	// The next call for the same tool is approved without prompting, so the
	// exhausted scanner is never read again.
	if !confirm(context.Background(), agent.ToolCall{Name: "edit"}, agent.ToolResult{}) {
		t.Fatal("allowlisted tool should be approved without a prompt")
	}
}

func TestConfirmToolDeniesByDefault(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("\n"))
	var out bytes.Buffer
	confirm := confirmTool(scanner, &out, map[string]bool{}, t.TempDir())
	if confirm(context.Background(), agent.ToolCall{Name: "bash"}, agent.ToolResult{}) {
		t.Fatal("empty answer should deny")
	}
}

func TestToolDiffPreviewShowsEditAndWriteChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}

	editArgs, _ := json.Marshal(map[string]any{"path": "a.txt", "old_string": "two", "new_string": "TWO"})
	preview := toolDiffPreview(agent.ToolCall{Name: "edit", Arguments: editArgs}, dir)
	if !strings.Contains(preview, "- two") || !strings.Contains(preview, "+ TWO") {
		t.Fatalf("edit preview = %q", preview)
	}

	writeArgs, _ := json.Marshal(map[string]any{"path": "a.txt", "content": "one\n2\nthree\n"})
	preview = toolDiffPreview(agent.ToolCall{Name: "write", Arguments: writeArgs}, dir)
	if !strings.Contains(preview, "- two") || !strings.Contains(preview, "+ 2") {
		t.Fatalf("write preview = %q", preview)
	}

	newFileArgs, _ := json.Marshal(map[string]any{"path": "new.txt", "content": "hello\n"})
	preview = toolDiffPreview(agent.ToolCall{Name: "write", Arguments: newFileArgs}, dir)
	if !strings.Contains(preview, "new file") || !strings.Contains(preview, "+ hello") {
		t.Fatalf("new-file preview = %q", preview)
	}

	if preview := toolDiffPreview(agent.ToolCall{Name: "read"}, dir); preview != "" {
		t.Fatalf("read preview = %q, want empty", preview)
	}
}

// The todo chain depends on the session store satisfying agent.TodoReader; a
// silent type-assertion failure would disable the whole feature.
func TestTodoReaderForWiresSessionStore(t *testing.T) {
	dir := t.TempDir()
	store := session.NewStore(dir)
	sess := store.Create("chain")
	sess.Todos = `[{"content":"task","status":"pending"}]`
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	todos := newSessionTodoStore(store, func() string { return sess.ID })

	enabled := config.Default()
	reader := todoReaderFor(enabled, todos)
	if reader == nil {
		t.Fatal("todoReaderFor() = nil, want a live reader")
	}
	if got := reader.Load(); !strings.Contains(got, "task") {
		t.Fatalf("reader.Load() = %q, want the stored list", got)
	}

	// agent.todo_chain: false turns the chain off.
	off := false
	disabled := config.Default()
	disabled.Agent.TodoChain = &off
	if reader := todoReaderFor(disabled, todos); reader != nil {
		t.Fatal("todoReaderFor() returned a reader while the chain is disabled")
	}
	if reader := todoReaderFor(enabled, nil); reader != nil {
		t.Fatal("todoReaderFor() returned a reader without a store")
	}
}
