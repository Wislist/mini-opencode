package tui

import (
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

func TestPermissionsCommand(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	m.width = 100
	m.allowToolForSession("bash")
	m.handleInput("/permissions")
	if !strings.Contains(strings.Join(m.blocks, "\n"), "帮我批准") {
		t.Fatal("missing modes")
	}
	m.handleInput("/permissions full-access")
	if m.PermissionMode() != agent.PermissionModeAsk {
		t.Fatal("full access without confirmation")
	}
	m.handleInput("/permissions full-access confirm")
	if m.PermissionMode() != agent.PermissionModeFullAccess {
		t.Fatal("confirmed mode not applied")
	}
	if len(m.SessionAllowedTools()) != 0 {
		t.Fatal("old grants retained")
	}
	m.handleInput("/permissions auto-review")
	if m.PermissionMode() != agent.PermissionModeAutoReview {
		t.Fatal("auto not applied")
	}
	m.handleInput("/permissions invalid")
	if m.PermissionMode() != agent.PermissionModeAutoReview {
		t.Fatal("invalid changed mode")
	}
	m.state = stateRunning
	m.handleInput("/permissions ask")
	if m.PermissionMode() != agent.PermissionModeAutoReview {
		t.Fatal("mode changed during run")
	}
}

func TestPermissionsDoNotMutatePersistedConfig(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	m.handleInput("/permissions full-access confirm")
	if cfg.Permissions.Mode != agent.PermissionModeAsk {
		t.Fatal("transient permission mode leaked into saved settings")
	}
}
