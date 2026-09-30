package app

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/config"
)

func TestCLIPermissions(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	in := "/permissions\n/permissions full-access\n/permissions\n/permissions full-access confirm\n/status\n/permissions auto-review\n/permissions ask\n/quit\n"
	if err := Run(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"替我审核", "full-access confirm", "permissions: full-access", "permissions: auto-review", "permissions: ask"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, out.String())
		}
	}
}

func TestCLIPermissionSwitchDoesNotPersistWithNameSave(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader("/permissions full-access confirm\n/name user Alice\n/quit\n"), &out); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permissions.Mode != "ask" {
		t.Fatalf("transient mode was saved: %s", cfg.Permissions.Mode)
	}
}
