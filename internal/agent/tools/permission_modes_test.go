package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

func TestFilePermissionModeSwitch(t *testing.T) {
	root := t.TempDir()
	p, _ := agent.NewModePermissionPolicy(root, nil, agent.PermissionModeAsk)
	opts := FileOptions{WorkDir: root, FullAccess: p.FullAccess}
	outside := filepath.Join(t.TempDir(), "file")
	if _, err := resolveWorkspacePathWithOptions(opts, outside); err == nil {
		t.Fatal("ask allowed outside")
	}
	_ = p.SetMode(agent.PermissionModeFullAccess)
	if got, err := resolveWorkspacePathWithOptions(opts, outside); err != nil || got != outside {
		t.Fatalf("full: %s %v", got, err)
	}
	if _, err := resolveWorkspacePathWithOptions(opts, ""); err == nil {
		t.Fatal("full accepted empty path")
	}
	_ = p.SetMode(agent.PermissionModeAutoReview)
	if _, err := resolveWorkspacePathWithOptions(opts, outside); err == nil {
		t.Fatal("downgrade kept full access")
	}
}

func TestCodingToolsFullAccessRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "outside.txt")
	p, _ := agent.NewModePermissionPolicy(root, nil, agent.PermissionModeFullAccess)
	registry := agent.NewToolRegistry()
	registry.SetPermissionPolicy(p)
	for _, tool := range CodingTools(CodingToolOptions{WorkDir: root, FullAccess: p.FullAccess}) {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	args, _ := json.Marshal(map[string]string{"path": path, "content": "hello"})
	if result := registry.Run(context.Background(), agent.ToolCall{Name: "write", Arguments: args}); result.Error != "" {
		t.Fatal(result.Error)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hello" {
		t.Fatalf("%q %v", data, err)
	}
	args, _ = json.Marshal(map[string]string{"path": path})
	if result := registry.Run(context.Background(), agent.ToolCall{Name: "read", Arguments: args}); result.Error != "" {
		t.Fatal(result.Error)
	}
	_ = p.SetMode(agent.PermissionModeAsk)
	if result := registry.RunApproved(context.Background(), agent.ToolCall{Name: "read", Arguments: args}); result.Metadata["permission"] != "deny" {
		t.Fatalf("downgrade bypassed: %+v", result)
	}
}

func TestBashFullAccessBypassesLegacyBans(t *testing.T) {
	p, _ := agent.NewModePermissionPolicy(t.TempDir(), nil, agent.PermissionModeAsk)
	bash := NewBashTool(BashOptions{FullAccess: p.FullAccess})
	if bash.bannedCommand("git push origin main") == "" {
		t.Fatal("ask lost ban")
	}
	_ = p.SetMode(agent.PermissionModeFullAccess)
	if bash.bannedCommand("git push origin main") != "" {
		t.Fatal("full kept legacy ban")
	}
	_ = p.SetMode(agent.PermissionModeAsk)
	if bash.bannedCommand("git push origin main") == "" {
		t.Fatal("downgrade lost ban")
	}
}
