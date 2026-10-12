package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveCanonicalProviders(t *testing.T) {
	for _, tc := range []struct {
		name, input, active string
		count               int
	}{
		{"empty", `{}`, "echo", 1},
		{"legacy", `{"provider":{"name":"relay","base_url":"https://relay.example/v1","model":"custom","context_window":1000000,"api_key_env":"RELAY_KEY"}}`, "relay", 1},
		{"catalog wins", `{"provider":{"name":"stale"},"providers":[{"name":"echo"},{"name":"relay","model":"custom","context_window":1000000}],"active_provider":"relay"}`, "relay", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			before := append([]ProviderConfig(nil), cfg.Providers...)
			// A stale runtime projection must never override the catalog on disk.
			cfg.Provider = ProviderConfig{Name: "stale"}
			if err := Save(path, cfg); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			if _, exists := raw["provider"]; exists {
				t.Fatal("saved legacy provider block")
			}
			loaded, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.ActiveProvider != tc.active || len(loaded.Providers) != tc.count {
				t.Fatalf("unexpected catalog: %+v", loaded.Providers)
			}
			if !reflect.DeepEqual(loaded.Providers, before) {
				t.Fatal("provider settings changed on save")
			}
			if cfg.Provider.Name != "stale" || !reflect.DeepEqual(cfg.Providers, before) {
				t.Fatal("Save mutated caller")
			}
			if err := Save(path, loaded); err != nil {
				t.Fatal(err)
			}
			again, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != string(data) {
				t.Fatal("save is not idempotent")
			}
		})
	}
}

func TestSaveNormalizesProviderOnlyAndZeroConfig(t *testing.T) {
	for _, cfg := range []Config{{}, {Provider: DefaultDeepSeekProvider()}} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := Save(path, cfg); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		if _, ok := raw["providers"]; !ok {
			t.Fatal("missing providers catalog")
		}
		if _, ok := raw["active_provider"]; !ok {
			t.Fatal("missing active_provider")
		}
	}
}

func TestSaveInvalidActiveProviderPreservesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	const original = "original content"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Providers: []ProviderConfig{{Name: "echo"}}, ActiveProvider: "missing"}
	if err := Save(path, cfg); err == nil {
		t.Fatal("expected invalid active provider error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatal("invalid configuration overwrote file")
	}
}
