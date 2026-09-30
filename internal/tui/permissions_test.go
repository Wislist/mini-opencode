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
	// Bare /permissions opens the picker as an overlay rather than printing the
	// choices into the transcript; see permissions_menu_test.go.
	m.handleInput("/permissions")
	if m.state != statePermissions {
		t.Fatal("bare /permissions did not open the picker")
	}
	if !strings.Contains(plainText(m.renderPermissionsMenu()), "替我审核") {
		t.Fatal("missing modes")
	}
	m = pressKey(m, key("esc"))
	if m.state != stateIdle {
		t.Fatal("esc did not close the picker")
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
