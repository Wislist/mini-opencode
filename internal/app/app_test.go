package app

import (
	"bufio"
	"bytes"
	"context"
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
	if !strings.Contains(got, "mini-opencode 0.2.0") {
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
	confirm := confirmTool(scanner, &out)

	if !confirm(context.Background(), agent.ToolCall{Name: "bash"}, agent.ToolResult{}) {
		t.Fatal("expected confirmation to be accepted")
	}
}
