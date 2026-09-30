package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// removedCommands are the slash commands the CLI and TUI no longer accept.
var removedCommands = []string{"/help", "/version", "/tools", "/plan", "/workspace", "/key", "/name"}

func TestCLIRemovedCommandsAreUnknown(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var out bytes.Buffer
	input := strings.Join([]string{
		"/help", "/version", "/tools", "/plan", "/workspace", "/key sk-abc", "/name bob", "/key", "/name", "/quit",
	}, "\n") + "\n"
	if err := Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	for _, cmd := range removedCommands {
		if !strings.Contains(got, "unknown command: "+cmd) {
			t.Fatalf("removed command %s was not rejected:\n%s", cmd, got)
		}
	}
	// They must not leave anything behind either: /key used to write a secret,
	// /name used to rewrite config.json.
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
		t.Fatal("a removed command rewrote config.json")
	}
	if _, err := os.Stat(filepath.Join(dir, ".mini-opencode", "secrets.json")); err == nil {
		t.Fatal("a removed command stored a secret")
	}
}

func TestCLIUnknownCommandHintDoesNotPointAtHelp(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("/nope\n/quit\n"), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "unknown command: /nope") {
		t.Fatalf("unknown command not reported:\n%s", got)
	}
	if strings.Contains(got, "/help") {
		t.Fatalf("the hint still points at the deleted /help:\n%s", got)
	}
}

func TestCLIStartupBannerListsOnlyRemainingCommands(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("/quit\n"), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var banner string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "commands:") {
			banner = line
		}
	}
	if banner == "" {
		t.Fatalf("no startup banner in:\n%s", out.String())
	}
	for _, cmd := range removedCommands {
		if strings.Contains(banner, cmd) {
			t.Fatalf("banner still advertises %s: %q", cmd, banner)
		}
	}
	for _, want := range []string{"/status", "/provider", "/model", "/permissions", "/compact", "/quit"} {
		if !strings.Contains(banner, want) {
			t.Fatalf("banner %q is missing %s", banner, want)
		}
	}
}
