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
)

func TestRunVersionThenQuit(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("/version\n/quit\n")

	if err := Run(context.Background(), in, &out); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "mini-opencode 0.3.0") {
		t.Fatalf("output missing version: %q", got)
	}
}

func TestRunToolsListsRegisteredTools(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var out bytes.Buffer
	in := strings.NewReader("/tools\n/quit\n")

	if err := Run(context.Background(), in, &out); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"bash", "read", "write", "edit", "glob", "grep"} {
		if !strings.Contains(got, want) {
			t.Fatalf("/tools output missing %s: %q", want, got)
		}
	}
}

func TestRunKeyCommandSavesLocalDeepSeekConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var out bytes.Buffer
	in := strings.NewReader("/key test-key\n/version\n/quit\n")

	if err := Run(context.Background(), in, &out); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "[deepseek key saved]") {
		t.Fatalf("output = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("config.json not saved: %v", err)
	}
	secretPath := filepath.Join(dir, ".mini-opencode", "secrets.json")
	data, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("secrets not saved: %v", err)
	}
	if !strings.Contains(string(data), "test-key") {
		t.Fatalf("secret store missing key: %q", data)
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
