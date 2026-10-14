package config

import (
	"encoding/json"
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
	if cfg.ProviderConfigured {
		t.Fatal("missing config must keep echo as an internal fallback, not mark it as user-configured")
	}
}

func TestLoadExplicitEchoCountsAsConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"echo","type":"echo"}],"active_provider":"echo"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.ProviderConfigured {
		t.Fatal("an explicitly configured echo provider must not be presented as first-run setup")
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

func TestLoadMemoryRetrievalConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{
  "provider": {"name":"echo"},
  "memory": {
    "retrieval":"hybrid",
    "embedding_provider":"relay",
    "embedding_model":"embed-small",
    "top_k":4,
    "candidate_k":15,
    "max_tokens":900,
    "min_similarity":0.42
  }
}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Memory.HybridEnabled() {
		t.Fatal("hybrid retrieval was not enabled")
	}
	if cfg.Memory.EmbeddingProvider != "relay" || cfg.Memory.EmbeddingModel != "embed-small" {
		t.Fatalf("memory config = %#v", cfg.Memory)
	}
	if cfg.Memory.EffectiveTopK() != 4 || cfg.Memory.EffectiveCandidateK() != 15 || cfg.Memory.EffectiveMaxTokens() != 900 {
		t.Fatalf("effective memory config = %#v", cfg.Memory)
	}
	if cfg.Memory.EffectiveMinSimilarity() != 0.42 {
		t.Fatalf("min similarity = %v", cfg.Memory.EffectiveMinSimilarity())
	}
}

func TestMemoryRetrievalDefaultsStayLexical(t *testing.T) {
	cfg := Default()
	if cfg.Memory.HybridEnabled() {
		t.Fatal("hybrid retrieval should require explicit opt-in")
	}
	if cfg.Memory.EffectiveTopK() != 3 || cfg.Memory.EffectiveCandidateK() != 12 || cfg.Memory.EffectiveMaxTokens() != 1600 {
		t.Fatalf("defaults = %#v", cfg.Memory)
	}
	if cfg.Memory.EffectiveMinSimilarity() != 0.35 {
		t.Fatalf("default min similarity = %v", cfg.Memory.EffectiveMinSimilarity())
	}
}

func TestProviderConfigResolvesAPIKeyFromEnv(t *testing.T) {
	t.Setenv("TEST_DEEPSEEK_KEY", "secret")
	cfg := ProviderConfig{APIKeyEnv: "TEST_DEEPSEEK_KEY"}
	if got := cfg.ResolvedAPIKey(); got != "secret" {
		t.Fatalf("ResolvedAPIKey() = %q", got)
	}
}

func TestProviderConfigInlineAPIKeyTakesPriorityOverEnv(t *testing.T) {
	t.Setenv("TEST_DEEPSEEK_KEY", "from-env")
	cfg := ProviderConfig{Name: "deepseek", APIKey: "from-config", APIKeyEnv: "TEST_DEEPSEEK_KEY"}
	if got := cfg.ResolvedAPIKey(); got != "from-config" {
		t.Fatalf("ResolvedAPIKey() = %q", got)
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
	if got := cfg.EffectiveContextWindow(); got != 1048576 {
		t.Fatalf("EffectiveContextWindow() = %d, want 1048576", got)
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
	cfgJSON, err := json.Marshal(map[string]any{
		"workspace": map[string]any{"allowed_roots": []string{dir, dir + string(os.PathSeparator)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, cfgJSON, 0644); err != nil {
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
