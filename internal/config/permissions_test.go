package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPermissionModes(t *testing.T) {
	for _, mode := range []string{"", "ask", "auto-review", "full-access", "typo"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(`{"permissions":{"mode":"`+mode+`"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if mode == "typo" {
				if err == nil {
					t.Fatal("unknown mode accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := mode
			if want == "" {
				want = "ask"
			}
			if string(cfg.Permissions.Mode) != want {
				t.Fatalf("mode=%s want=%s", cfg.Permissions.Mode, want)
			}
			if err := Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path)
			if err != nil || loaded.Permissions != cfg.Permissions {
				t.Fatal("round trip", loaded.Permissions, err)
			}
		})
	}
}
