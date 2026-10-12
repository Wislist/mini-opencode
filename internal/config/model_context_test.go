package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestModelContextParsing(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  int
		valid bool
	}{
		{"1M", 1000000, true}, {"128k", 128000, true}, {"1", 1, true}, {"1000000", 1000000, true}, {"auto", 0, true},
		{"0", 0, false}, {"-1", 0, false}, {"1.5M", 0, false}, {"999999999999999999999M", 0, false}, {"1M extra", 0, false},
	} {
		t.Run(tt.value, func(t *testing.T) {
			cmd, err := ParseModelCommand("/model context " + tt.value)
			if (err == nil) != tt.valid {
				t.Fatalf("error = %v", err)
			}
			if tt.valid && (!cmd.Context || cmd.Window == nil || *cmd.Window != tt.want) {
				t.Fatalf("command = %+v", cmd)
			}
		})
	}
	cmd, err := ParseModelCommand("/model context")
	if err != nil || !cmd.Context || cmd.Window != nil {
		t.Fatalf("query = %+v, %v", cmd, err)
	}
}

func TestModelContextPersistenceAndRollback(t *testing.T) {
	cfg := Default()
	if err := cfg.AddProvider(ProviderConfig{Name: "relay", Model: "custom", BaseURL: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetActiveProvider("relay"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	cmd, _ := ParseModelCommand("/model context 1M")
	changed, _, err := cfg.ApplyModelContext(cmd, path)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.Provider.ContextWindow != 1000000 {
		t.Fatalf("reload window=%d err=%v", loaded.Provider.ContextWindow, err)
	}
	before := cfg
	before.Providers = append([]ProviderConfig(nil), cfg.Providers...)
	auto, _ := ParseModelCommand("/model context auto")
	if _, _, err := cfg.ApplyModelContext(auto, t.TempDir()); err == nil {
		t.Fatal("expected save failure")
	}
	if !reflect.DeepEqual(cfg, before) {
		t.Fatal("failed save mutated config")
	}
	if changed, _, err := cfg.ApplyModelContext(cmd, path); err != nil || changed {
		t.Fatalf("repeat changed=%v err=%v", changed, err)
	}
	if _, _, err := cfg.ApplyModelContext(auto, path); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path)
	if err != nil || loaded.Provider.ContextWindow != 0 {
		t.Fatalf("auto reload: %+v %v", loaded.Provider, err)
	}
}
