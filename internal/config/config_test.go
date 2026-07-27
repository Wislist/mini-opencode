package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingConfigUsesEchoDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider.Name != "echo" {
		t.Fatalf("provider = %q", cfg.Provider.Name)
	}
}

func TestLoadAppliesDisplayNameDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":{"name":"echo"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.User != "you" {
		t.Fatalf("User = %q, want \"you\"", cfg.User)
	}
	if cfg.Assistant != "assistant" {
		t.Fatalf("Assistant = %q, want \"assistant\"", cfg.Assistant)
	}
}

func TestLoadPreservesCustomDisplayNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":{"name":"echo"},"user":"Alice","assistant":"Codex"}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.User != "Alice" || cfg.Assistant != "Codex" {
		t.Fatalf("names = %q/%q, want Alice/Codex", cfg.User, cfg.Assistant)
	}
}

func TestProviderConfigResolvesAPIKeyFromEnv(t *testing.T) {
	t.Setenv("TEST_DEEPSEEK_KEY", "secret")
	cfg := ProviderConfig{APIKeyEnv: "TEST_DEEPSEEK_KEY"}
	if got := cfg.ResolvedAPIKey(); got != "secret" {
		t.Fatalf("ResolvedAPIKey() = %q", got)
	}
}

func TestProviderConfigResolvesAPIKeyFromLocalStore(t *testing.T) {
	dir := t.TempDir()
	if err := SaveProviderKey(dir, "deepseek", "local-secret"); err != nil {
		t.Fatalf("SaveProviderKey() error = %v", err)
	}
	cfg := ProviderConfig{Name: "deepseek"}
	if got := cfg.ResolvedAPIKeyFrom(dir); got != "local-secret" {
		t.Fatalf("ResolvedAPIKeyFrom() = %q", got)
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":{"name":"deepseek","model":"deepseek-chat"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider.Name != "deepseek" || cfg.Provider.Model != "deepseek-chat" {
		t.Fatalf("config = %#v", cfg.Provider)
	}
}

func TestSaveWritesConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	err := Save(path, Config{Provider: DefaultDeepSeekProvider()})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Provider.Name != "deepseek" {
		t.Fatalf("provider = %q", cfg.Provider.Name)
	}
}

func TestEffectiveContextWindowUsesConfiguredValue(t *testing.T) {
	cfg := ProviderConfig{ContextWindow: 32000}
	if got := cfg.EffectiveContextWindow(); got != 32000 {
		t.Fatalf("EffectiveContextWindow() = %d, want 32000", got)
	}
}

func TestEffectiveContextWindowFallsBackToModelDefault(t *testing.T) {
	cfg := ProviderConfig{Model: "deepseek-chat"}
	if got := cfg.EffectiveContextWindow(); got != 64000 {
		t.Fatalf("EffectiveContextWindow() = %d, want 64000", got)
	}
}

func TestEffectiveContextWindowUnknownModelDefault(t *testing.T) {
	cfg := ProviderConfig{Model: "some-custom-model"}
	if got := cfg.EffectiveContextWindow(); got != 8192 {
		t.Fatalf("EffectiveContextWindow() = %d, want 8192", got)
	}
}

func TestNormalizeAllowedRootsDedupesAndCleans(t *testing.T) {
	dir := t.TempDir()
	got := normalizeAllowedRoots([]string{dir, dir + "/", "  ", ""})
	if len(got) != 1 {
		t.Fatalf("got = %#v, want 1 entry", got)
	}
	if got[0] != filepath.Clean(dir) {
		t.Fatalf("got[0] = %q, want %q", got[0], filepath.Clean(dir))
	}
}

func TestLoadNormalizesAllowedRoots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfgJSON := `{"workspace":{"allowed_roots":["` + dir + `","` + dir + `/"]}}`
	if err := os.WriteFile(path, []byte(cfgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Workspace.AllowedRoots) != 1 {
		t.Fatalf("allowed roots = %#v, want 1", cfg.Workspace.AllowedRoots)
	}
}
