package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeConfig drops a config.json into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadLegacySingleProviderBlockStaysCompatible(t *testing.T) {
	path := writeConfig(t, `{
	  "provider": {
	    "name": "deepseek",
	    "base_url": "https://api.deepseek.com",
	    "model": "deepseek-chat",
	    "api_key_env": "DEEPSEEK_API_KEY"
	  }
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.Name != "deepseek" {
		t.Fatalf("Provider.Name = %q, want deepseek", cfg.Provider.Name)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("len(Providers) = %d, want 1 (the legacy block becomes the catalog)", len(cfg.Providers))
	}
	if !reflect.DeepEqual(cfg.Providers[0], cfg.Provider) {
		t.Fatalf("Providers[0] = %+v, want it to equal the resolved Provider %+v", cfg.Providers[0], cfg.Provider)
	}
	if cfg.ActiveProvider != "deepseek" {
		t.Fatalf("ActiveProvider = %q, want deepseek", cfg.ActiveProvider)
	}
	if got := cfg.Provider.EffectiveType(); got != ProviderTypeOpenAICompatible {
		t.Fatalf("EffectiveType() = %q, want %q", got, ProviderTypeOpenAICompatible)
	}
}

func TestLoadProvidersArrayWithCustomRelay(t *testing.T) {
	path := writeConfig(t, `{
	  "providers": [
	    {
	      "name": "my-relay",
	      "label": "我的中转",
	      "base_url": "https://relay.example.com/v1",
	      "api_key": "sk-inline",
	      "models": ["gpt-4o", "deepseek-chat"],
	      "model": "gpt-4o",
	      "context_window": 200000
	    },
	    { "name": "echo", "type": "echo" }
	  ],
	  "active_provider": "my-relay"
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("len(Providers) = %d, want 2", len(cfg.Providers))
	}
	if cfg.Provider.Name != "my-relay" {
		t.Fatalf("Provider.Name = %q, want my-relay", cfg.Provider.Name)
	}
	if cfg.Provider.DisplayName() != "我的中转" {
		t.Fatalf("DisplayName() = %q, want 我的中转", cfg.Provider.DisplayName())
	}
	if cfg.Provider.BaseURL != "https://relay.example.com/v1" {
		t.Fatalf("BaseURL = %q", cfg.Provider.BaseURL)
	}
	if got := cfg.Provider.ResolvedAPIKey(); got != "sk-inline" {
		t.Fatalf("ResolvedAPIKey() = %q, want sk-inline", got)
	}
	if cfg.Provider.EffectiveContextWindow() != 200000 {
		t.Fatalf("EffectiveContextWindow() = %d, want 200000", cfg.Provider.EffectiveContextWindow())
	}
	models := cfg.Provider.CatalogModels()
	want := []string{"gpt-4o", "deepseek-chat"}
	if strings.Join(models, ",") != strings.Join(want, ",") {
		t.Fatalf("CatalogModels() = %v, want %v", models, want)
	}
	if cfg.Providers[1].EffectiveType() != ProviderTypeEcho {
		t.Fatalf("echo entry EffectiveType() = %q", cfg.Providers[1].EffectiveType())
	}
}

// A display name with no label falls back to the provider name, so every menu
// row has something to show.
func TestDisplayNameFallsBackToName(t *testing.T) {
	p := ProviderConfig{Name: "relay"}
	if got := p.DisplayName(); got != "relay" {
		t.Fatalf("DisplayName() = %q, want relay", got)
	}
	if got := (ProviderConfig{Name: "relay", Label: "   "}).DisplayName(); got != "relay" {
		t.Fatalf("blank label DisplayName() = %q, want relay", got)
	}
}

func TestLoadActiveProviderIsCaseInsensitive(t *testing.T) {
	path := writeConfig(t, `{
	  "providers": [{"name": "Relay", "base_url": "https://r.example.com/v1"}, {"name": "other", "base_url": "https://o.example.com/v1"}],
	  "active_provider": "RELAY"
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.Name != "Relay" {
		t.Fatalf("Provider.Name = %q, want the canonical spelling Relay", cfg.Provider.Name)
	}
}

func TestLoadUnknownActiveProviderFails(t *testing.T) {
	path := writeConfig(t, `{
	  "providers": [{"name": "a", "base_url": "https://a.example.com/v1"}],
	  "active_provider": "typo"
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load = nil error, want a failure naming the unknown provider")
	}
	for _, want := range []string{"unknown active_provider", "typo", "a"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestLoadWithoutActiveProviderUsesFirstEntry(t *testing.T) {
	path := writeConfig(t, `{
	  "providers": [{"name": "first", "base_url": "https://f.example.com/v1"}, {"name": "second", "base_url": "https://s.example.com/v1"}]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.Name != "first" {
		t.Fatalf("Provider.Name = %q, want first", cfg.Provider.Name)
	}
	if cfg.ActiveProvider != "first" {
		t.Fatalf("ActiveProvider = %q, want first", cfg.ActiveProvider)
	}
}

// When both formats are present the array is authoritative: otherwise a
// half-migrated config would silently keep two competing sources of truth.
func TestLoadProvidersArraySupersedesLegacyBlock(t *testing.T) {
	path := writeConfig(t, `{
	  "provider": {"name": "deepseek", "base_url": "https://api.deepseek.com"},
	  "providers": [{"name": "relay", "base_url": "https://relay.example.com/v1"}]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("len(Providers) = %d, want 1 (legacy block ignored)", len(cfg.Providers))
	}
	if cfg.Provider.Name != "relay" {
		t.Fatalf("Provider.Name = %q, want relay", cfg.Provider.Name)
	}
	if _, ok := cfg.ProviderByName("deepseek"); ok {
		t.Fatal("the legacy block leaked into the catalog")
	}
}

func TestLoadDefaultsToEchoWithoutProviderConfig(t *testing.T) {
	path := writeConfig(t, `{}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.Name != "echo" || cfg.Provider.EffectiveType() != ProviderTypeEcho {
		t.Fatalf("Provider = %+v, want the echo fallback", cfg.Provider)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("len(Providers) = %d, want 1", len(cfg.Providers))
	}
}

// Missing file is not an error: the built-in defaults apply.
func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider.EffectiveType() != ProviderTypeEcho {
		t.Fatalf("Provider = %+v, want echo", cfg.Provider)
	}
}

// Boundary: blank names, duplicate names, padded names and padded model lists
// all come out normalized, so a picker built from the catalog has no empty or
// doubled rows.
func TestLoadNormalizesProviderCatalog(t *testing.T) {
	path := writeConfig(t, `{
	  "providers": [
	    {"name": "  relay  ", "base_url": "https://relay.example.com/v1"},
	    {"name": "   ", "base_url": "https://blank.example.com/v1"},
	    {"name": "", "base_url": "https://empty.example.com/v1"},
	    {"name": "RELAY", "base_url": "https://dupe.example.com/v1"},
	    {"name": "second", "base_url": "https://second.example.com/v1", "models": ["  m1 ", "", "m2", "m1", "   "]}
	  ]
	}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("len(Providers) = %d, want 2 (blank names dropped, duplicates merged)", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "relay" {
		t.Fatalf("Providers[0].Name = %q, want relay (trimmed)", cfg.Providers[0].Name)
	}
	if cfg.Providers[0].BaseURL != "https://relay.example.com/v1" {
		t.Fatalf("first occurrence should win, got %q", cfg.Providers[0].BaseURL)
	}
	if got := cfg.Providers[1].CatalogModels(); strings.Join(got, ",") != "m1,m2" {
		t.Fatalf("CatalogModels() = %v, want [m1 m2]", got)
	}
}

func TestLoadRejectsUnknownProviderType(t *testing.T) {
	path := writeConfig(t, `{
	  "providers": [{"name": "x", "type": "anthropic", "base_url": "https://x.example.com/v1"}]
	}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load = nil error, want a failure for an unsupported type")
	}
	if !strings.Contains(err.Error(), "anthropic") {
		t.Fatalf("error %q should name the offending type", err.Error())
	}
}

func TestEffectiveTypeInference(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ProviderConfig
		want    string
		wantErr bool
	}{
		{name: "echo by name", cfg: ProviderConfig{Name: "echo"}, want: ProviderTypeEcho},
		{name: "echo case insensitive", cfg: ProviderConfig{Name: "Echo"}, want: ProviderTypeEcho},
		{name: "custom name defaults to openai compatible", cfg: ProviderConfig{Name: "my-relay"}, want: ProviderTypeOpenAICompatible},
		{name: "legacy deepseek name", cfg: ProviderConfig{Name: "deepseek"}, want: ProviderTypeOpenAICompatible},
		{name: "explicit echo", cfg: ProviderConfig{Name: "anything", Type: "echo"}, want: ProviderTypeEcho},
		{name: "explicit openai alias", cfg: ProviderConfig{Name: "r", Type: "openai"}, want: ProviderTypeOpenAICompatible},
		{name: "explicit underscore alias", cfg: ProviderConfig{Name: "r", Type: "openai_compatible"}, want: ProviderTypeOpenAICompatible},
		{name: "explicit padded upper", cfg: ProviderConfig{Name: "r", Type: " OpenAI-Compatible "}, want: ProviderTypeOpenAICompatible},
		{name: "empty name and type", cfg: ProviderConfig{}, want: ProviderTypeEcho},
		{name: "unsupported", cfg: ProviderConfig{Name: "r", Type: "anthropic"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.cfg.NormalizedType()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizedType() = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizedType(): %v", err)
			}
			if got != tc.want {
				t.Fatalf("NormalizedType() = %q, want %q", got, tc.want)
			}
			if tc.cfg.EffectiveType() != tc.want {
				t.Fatalf("EffectiveType() = %q, want %q (must not error)", tc.cfg.EffectiveType(), tc.want)
			}
		})
	}
}

func TestCatalogModelsEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		cfg  ProviderConfig
		want string
	}{
		{name: "empty everything", cfg: ProviderConfig{}, want: ""},
		{name: "only the active model", cfg: ProviderConfig{Model: "gpt-4o"}, want: "gpt-4o"},
		{name: "active model appended when missing", cfg: ProviderConfig{Models: []string{"a", "b"}, Model: "c"}, want: "a,b,c"},
		{name: "active model already listed", cfg: ProviderConfig{Models: []string{"a", "b"}, Model: "a"}, want: "a,b"},
		{name: "padded active model", cfg: ProviderConfig{Models: []string{"a"}, Model: "  a  "}, want: "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(tc.cfg.CatalogModels(), ",")
			if got != tc.want {
				t.Fatalf("CatalogModels() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProviderByName(t *testing.T) {
	cfg := Config{Providers: []ProviderConfig{{Name: "Relay", BaseURL: "https://r"}, {Name: "other"}}}
	if p, ok := cfg.ProviderByName("relay"); !ok || p.Name != "Relay" {
		t.Fatalf("ProviderByName(relay) = %+v, %v", p, ok)
	}
	if p, ok := cfg.ProviderByName("  OTHER "); !ok || p.Name != "other" {
		t.Fatalf("padded lookup = %+v, %v", p, ok)
	}
	if _, ok := cfg.ProviderByName("missing"); ok {
		t.Fatal("unknown name reported as found")
	}
	if _, ok := cfg.ProviderByName(""); ok {
		t.Fatal("empty name reported as found")
	}
}

func TestSetActiveProvider(t *testing.T) {
	cfg := Config{Providers: []ProviderConfig{
		{Name: "a", BaseURL: "https://a"},
		{Name: "b", BaseURL: "https://b"},
	}}
	if err := cfg.SetActiveProvider("b"); err != nil {
		t.Fatalf("SetActiveProvider: %v", err)
	}
	if cfg.Provider.Name != "b" || cfg.ActiveProvider != "b" {
		t.Fatalf("active = %q / %q, want b / b", cfg.Provider.Name, cfg.ActiveProvider)
	}
	// Idempotent: switching to the current provider is not an error.
	if err := cfg.SetActiveProvider("b"); err != nil {
		t.Fatalf("second SetActiveProvider: %v", err)
	}
	if err := cfg.SetActiveProvider("nope"); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if err := cfg.SetActiveProvider(""); err == nil {
		t.Fatal("empty provider name accepted")
	}
	// The failure must not have changed the active provider.
	if cfg.Provider.Name != "b" {
		t.Fatalf("Provider.Name = %q after a failed switch, want b", cfg.Provider.Name)
	}
}

func TestAddProviderUpsertsAndActivates(t *testing.T) {
	cfg := Config{Providers: []ProviderConfig{{Name: "echo", Type: "echo"}}}

	if err := cfg.AddProvider(ProviderConfig{Name: "  relay  ", BaseURL: "https://relay.example.com/v1"}); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("len(Providers) = %d, want 2", len(cfg.Providers))
	}
	if cfg.Providers[1].Name != "relay" {
		t.Fatalf("added name = %q, want relay (trimmed)", cfg.Providers[1].Name)
	}
	if cfg.Provider.Name != "relay" || cfg.ActiveProvider != "relay" {
		t.Fatalf("adding did not activate: %q / %q", cfg.Provider.Name, cfg.ActiveProvider)
	}

	// Re-adding the same name updates in place instead of growing the catalog,
	// so running the command twice is not a way to accumulate duplicates.
	if err := cfg.AddProvider(ProviderConfig{Name: "RELAY", BaseURL: "https://new.example.com/v1"}); err != nil {
		t.Fatalf("re-AddProvider: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("len(Providers) = %d after re-add, want 2", len(cfg.Providers))
	}
	if cfg.Providers[1].BaseURL != "https://new.example.com/v1" {
		t.Fatalf("BaseURL = %q, want the updated value", cfg.Providers[1].BaseURL)
	}
	if cfg.Providers[1].Name != "relay" {
		t.Fatalf("re-add changed the canonical name to %q", cfg.Providers[1].Name)
	}
}

func TestAddProviderRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		in   ProviderConfig
	}{
		{name: "blank name", in: ProviderConfig{Name: "   ", BaseURL: "https://x"}},
		{name: "name with whitespace", in: ProviderConfig{Name: "my relay", BaseURL: "https://x"}},
		{name: "unsupported type", in: ProviderConfig{Name: "x", Type: "anthropic", BaseURL: "https://x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{}
			if err := cfg.AddProvider(tc.in); err == nil {
				t.Fatal("AddProvider = nil error, want a rejection")
			}
			if len(cfg.Providers) != 0 {
				t.Fatalf("rejected provider still landed in the catalog: %+v", cfg.Providers)
			}
			if cfg.ActiveProvider != "" {
				t.Fatalf("rejected provider became active: %q", cfg.ActiveProvider)
			}
		})
	}
}

// A custom provider is only usable when its name, endpoint and key travel
// together through a save/load round trip.
func TestSaveLoadRoundTripPreservesCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := Config{}
	if err := cfg.AddProvider(ProviderConfig{
		Name:    "relay",
		Label:   "中转",
		BaseURL: "https://relay.example.com/v1",
		Models:  []string{"m1", "m2"},
		Model:   "m2",
	}); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if err := cfg.AddProvider(ProviderConfig{Name: "backup", BaseURL: "https://backup.example.com/v1"}); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	// Go back to the first one: active_provider is the only thing that records
	// the choice, so it must survive the round trip.
	if err := cfg.SetActiveProvider("relay"); err != nil {
		t.Fatalf("SetActiveProvider: %v", err)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Providers) != 2 {
		t.Fatalf("len(Providers) = %d, want 2", len(loaded.Providers))
	}
	if loaded.Provider.Name != "relay" {
		t.Fatalf("active provider = %q, want relay", loaded.Provider.Name)
	}
	if loaded.Provider.Label != "中转" || loaded.Provider.Model != "m2" {
		t.Fatalf("provider fields lost: %+v", loaded.Provider)
	}
}

// Secrets are keyed by provider name, which is what lets a third-party relay
// keep its own key next to the others.
func TestSecretsAreScopedByProviderName(t *testing.T) {
	dir := t.TempDir()
	if err := SaveProviderKey(dir, "relay", "sk-relay"); err != nil {
		t.Fatalf("SaveProviderKey: %v", err)
	}
	if err := SaveProviderKey(dir, "other", "sk-other"); err != nil {
		t.Fatalf("SaveProviderKey: %v", err)
	}
	store, err := LoadSecretStore(dir)
	if err != nil {
		t.Fatalf("LoadSecretStore: %v", err)
	}
	if store.ProviderKeys["relay"] != "sk-relay" || store.ProviderKeys["other"] != "sk-other" {
		t.Fatalf("store = %+v, want both keys", store.ProviderKeys)
	}

	relay := ProviderConfig{Name: "relay"}
	if got := relay.ResolvedAPIKeyFrom(dir); got != "sk-relay" {
		t.Fatalf("relay key = %q, want sk-relay", got)
	}
	if got := (ProviderConfig{Name: "unknown"}).ResolvedAPIKeyFrom(dir); got != "" {
		t.Fatalf("unknown provider key = %q, want empty", got)
	}
	if got := (ProviderConfig{}).ResolvedAPIKeyFrom(dir); got != "" {
		t.Fatalf("empty provider name key = %q, want empty", got)
	}
}

func TestAPIKeyPrecedence(t *testing.T) {
	dir := t.TempDir()
	if err := SaveProviderKey(dir, "relay", "sk-stored"); err != nil {
		t.Fatalf("SaveProviderKey: %v", err)
	}
	t.Setenv("MINI_OPENCODE_TEST_RELAY_KEY", "sk-env")

	inline := ProviderConfig{Name: "relay", APIKey: "sk-inline", APIKeyEnv: "MINI_OPENCODE_TEST_RELAY_KEY"}
	if got := inline.ResolvedAPIKeyFrom(dir); got != "sk-inline" {
		t.Fatalf("inline key = %q, want sk-inline", got)
	}
	fromEnv := ProviderConfig{Name: "relay", APIKeyEnv: "MINI_OPENCODE_TEST_RELAY_KEY"}
	if got := fromEnv.ResolvedAPIKeyFrom(dir); got != "sk-env" {
		t.Fatalf("env key = %q, want sk-env", got)
	}
	stored := ProviderConfig{Name: "relay"}
	if got := stored.ResolvedAPIKeyFrom(dir); got != "sk-stored" {
		t.Fatalf("stored key = %q, want sk-stored", got)
	}
	missing := ProviderConfig{Name: "relay", APIKeyEnv: "MINI_OPENCODE_TEST_UNSET_KEY"}
	if got := missing.ResolvedAPIKeyFrom(dir); got != "sk-stored" {
		t.Fatalf("unset env should fall back to the store, got %q", got)
	}
}

// The key source is what explains "why did switching to this provider fail",
// so each branch has to be distinguishable.
func TestProviderKeySource(t *testing.T) {
	dir := t.TempDir()
	if err := SaveProviderKey(dir, "stored", "sk"); err != nil {
		t.Fatalf("SaveProviderKey: %v", err)
	}
	t.Setenv("MINI_OPENCODE_TEST_KEY_SOURCE", "sk-env")

	cases := []struct {
		name string
		cfg  ProviderConfig
		want string
	}{
		{name: "echo", cfg: ProviderConfig{Name: "echo"}, want: "n/a"},
		{name: "inline", cfg: ProviderConfig{Name: "relay", APIKey: "sk"}, want: "config"},
		{name: "env", cfg: ProviderConfig{Name: "relay", APIKeyEnv: "MINI_OPENCODE_TEST_KEY_SOURCE"}, want: "env"},
		{name: "stored", cfg: ProviderConfig{Name: "stored"}, want: "secrets"},
		{name: "missing", cfg: ProviderConfig{Name: "nothing"}, want: "missing"},
		{name: "env unset", cfg: ProviderConfig{Name: "nothing", APIKeyEnv: "MINI_OPENCODE_TEST_UNSET"}, want: "未设置"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.KeySource(dir)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("KeySource() = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestProviderSummary(t *testing.T) {
	dir := t.TempDir()

	relay := ProviderConfig{Name: "relay", Label: "中转", BaseURL: "https://relay.example.com/v1", Model: "gpt-4o"}
	summary := relay.Summary(dir)
	for _, want := range []string{"中转", "relay", "https://relay.example.com/v1", "gpt-4o", "key=missing"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("Summary() = %q, missing %q", summary, want)
		}
	}
	echo := ProviderConfig{Name: "echo", Type: "echo"}
	if got := echo.Summary(dir); !strings.Contains(got, "本地回显") {
		t.Fatalf("echo Summary() = %q", got)
	}
}

func TestProviderValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ProviderConfig
		wantErr string
	}{
		{name: "relay", cfg: ProviderConfig{Name: "relay", BaseURL: "https://relay.example.com/v1"}},
		{name: "echo needs no url", cfg: ProviderConfig{Name: "echo", Type: "echo"}},
		{name: "implicit echo", cfg: ProviderConfig{Name: "echo"}},
		{name: "blank name", cfg: ProviderConfig{Name: "   ", BaseURL: "https://x.example.com"}, wantErr: "name is required"},
		{name: "padded name is accepted", cfg: ProviderConfig{Name: "  relay  ", BaseURL: "https://x.example.com"}},
		{name: "whitespace in name", cfg: ProviderConfig{Name: "my relay", BaseURL: "https://x.example.com"}, wantErr: "whitespace"},
		{name: "unsupported type", cfg: ProviderConfig{Name: "r", Type: "anthropic", BaseURL: "https://x.example.com"}, wantErr: "anthropic"},
		{name: "missing url", cfg: ProviderConfig{Name: "relay"}, wantErr: "base_url"},
		{name: "relative url", cfg: ProviderConfig{Name: "relay", BaseURL: "relay.example.com/v1"}, wantErr: "base_url"},
		{name: "non http scheme", cfg: ProviderConfig{Name: "relay", BaseURL: "ftp://relay.example.com"}, wantErr: "base_url"},
		{name: "hostless url", cfg: ProviderConfig{Name: "relay", BaseURL: "https://"}, wantErr: "base_url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// Adding a provider without an endpoint is a config that can only fail later,
// so it is rejected at the point where the user can still fix it.
func TestAddProviderRequiresEndpoint(t *testing.T) {
	cfg := Config{}
	if err := cfg.AddProvider(ProviderConfig{Name: "relay"}); err == nil {
		t.Fatal("AddProvider accepted a provider without base_url")
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("rejected provider landed in the catalog: %+v", cfg.Providers)
	}
}

func TestMergeModels(t *testing.T) {
	cases := []struct {
		name    string
		known   []string
		fetched []string
		want    string
	}{
		{name: "both empty"},
		{name: "only known", known: []string{"a", "b"}, want: "a,b"},
		{name: "only fetched", fetched: []string{"x"}, want: "x"},
		{name: "union keeps known order first", known: []string{"b", "a"}, fetched: []string{"a", "c"}, want: "b,a,c"},
		{name: "padded and blank entries", known: []string{" a "}, fetched: []string{"", "  ", "a", "b"}, want: "a,b"},
		{name: "known is nil", fetched: []string{"a", "a"}, want: "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(MergeModels(tc.known, tc.fetched), ",")
			if got != tc.want {
				t.Fatalf("MergeModels(%v, %v) = %q, want %q", tc.known, tc.fetched, got, tc.want)
			}
		})
	}
}

func TestSetProviderModels(t *testing.T) {
	newConfig := func(t *testing.T) Config {
		t.Helper()
		cfg := Config{}
		if err := cfg.AddProvider(ProviderConfig{Name: "relay", BaseURL: "https://relay.example.com/v1"}); err != nil {
			t.Fatalf("AddProvider: %v", err)
		}
		return cfg
	}

	t.Run("records models and picks the first as active", func(t *testing.T) {
		cfg := newConfig(t)
		changed, err := cfg.SetProviderModels("relay", []string{"gpt-4o", "deepseek-chat"}, "")
		if err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if !changed {
			t.Fatal("changed = false, want true")
		}
		if got := cfg.Provider.Model; got != "gpt-4o" {
			t.Fatalf("Model = %q, want the first offered model", got)
		}
		if got := strings.Join(cfg.Provider.CatalogModels(), ","); got != "gpt-4o,deepseek-chat" {
			t.Fatalf("CatalogModels() = %q", got)
		}
	})

	t.Run("keeps the active model when it is still offered", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderModels("relay", []string{"a", "b"}, "b"); err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if _, err := cfg.SetProviderModels("relay", []string{"a", "b", "c"}, ""); err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if got := cfg.Provider.Model; got != "b" {
			t.Fatalf("Model = %q, want b kept", got)
		}
	})

	t.Run("moves off a model that is no longer offered", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderModels("relay", []string{"a", "b"}, "b"); err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if _, err := cfg.SetProviderModels("relay", []string{"x"}, ""); err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if got := cfg.Provider.Model; got != "x" {
			t.Fatalf("Model = %q, want the only offered model", got)
		}
	})

	t.Run("preferred wins when offered", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderModels("relay", []string{"a", "b"}, "b"); err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if got := cfg.Provider.Model; got != "b" {
			t.Fatalf("Model = %q, want b", got)
		}
	})

	t.Run("ignores a preferred model that is not offered", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderModels("relay", []string{"a"}, "zzz"); err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if got := cfg.Provider.Model; got != "a" {
			t.Fatalf("Model = %q, want a", got)
		}
	})

	t.Run("empty list keeps the recorded model", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderModels("relay", []string{"a"}, ""); err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		changed, err := cfg.SetProviderModels("relay", nil, "")
		if err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if !changed {
			t.Fatal("clearing the list should report a change")
		}
		if got := cfg.Provider.Model; got != "a" {
			t.Fatalf("Model = %q, want the previous value kept", got)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderModels("relay", []string{"a", "b"}, ""); err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		changed, err := cfg.SetProviderModels("relay", []string{"a", "b"}, "")
		if err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if changed {
			t.Fatal("re-applying the same set reported a change")
		}
	})

	t.Run("updates a non-active provider without touching the active one", func(t *testing.T) {
		cfg := newConfig(t)
		if err := cfg.AddProvider(ProviderConfig{Name: "backup", BaseURL: "https://backup.example.com/v1", Model: "keep-me"}); err != nil {
			t.Fatalf("AddProvider: %v", err)
		}
		if err := cfg.SetActiveProvider("relay"); err != nil {
			t.Fatalf("SetActiveProvider: %v", err)
		}
		if _, err := cfg.SetProviderModels("backup", []string{"x"}, ""); err != nil {
			t.Fatalf("SetProviderModels: %v", err)
		}
		if cfg.Provider.Name != "relay" {
			t.Fatalf("active provider changed to %q", cfg.Provider.Name)
		}
		backup, _ := cfg.ProviderByName("backup")
		if backup.Model != "x" {
			t.Fatalf("backup model = %q, want x", backup.Model)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderModels("nope", []string{"a"}, ""); err == nil {
			t.Fatal("unknown provider accepted")
		}
	})
}

// 窗口是猜的时候必须能分辨：ctx 指示器据此标注「估值」，否则用户看到 57% 会
// 以为自己的模型只有 8k。
func TestContextWindowSource(t *testing.T) {
	cases := []struct {
		name string
		cfg  ProviderConfig
		want string
	}{
		{name: "explicit config wins", cfg: ProviderConfig{Model: "qwen3:30b", ContextWindow: 200000}, want: ContextWindowSourceConfig},
		{name: "built-in table", cfg: ProviderConfig{Model: "gpt-4o"}, want: ContextWindowSourceModel},
		{name: "deepseek table", cfg: ProviderConfig{Model: "deepseek-chat"}, want: ContextWindowSourceModel},
		{name: "custom model is a guess", cfg: ProviderConfig{Model: "qwen3:30b"}, want: ContextWindowSourceGuess},
		{name: "no model is a guess", cfg: ProviderConfig{}, want: ContextWindowSourceGuess},
		{name: "blank model is a guess", cfg: ProviderConfig{Model: "   "}, want: ContextWindowSourceGuess},
		{name: "explicit on a known model still wins", cfg: ProviderConfig{Model: "gpt-4o", ContextWindow: 64000}, want: ContextWindowSourceConfig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.ContextWindowSource(); got != tc.want {
				t.Fatalf("ContextWindowSource() = %q, want %q", got, tc.want)
			}
			// The three sources must agree with the number they describe.
			switch tc.want {
			case ContextWindowSourceConfig:
				if tc.cfg.EffectiveContextWindow() != tc.cfg.ContextWindow {
					t.Fatalf("EffectiveContextWindow() = %d, want the configured %d",
						tc.cfg.EffectiveContextWindow(), tc.cfg.ContextWindow)
				}
			case ContextWindowSourceGuess:
				if tc.cfg.EffectiveContextWindow() != guessContextWindow {
					t.Fatalf("EffectiveContextWindow() = %d, want the placeholder %d",
						tc.cfg.EffectiveContextWindow(), guessContextWindow)
				}
			}
			if tc.cfg.ContextWindowIsGuess() != (tc.want == ContextWindowSourceGuess) {
				t.Fatalf("ContextWindowIsGuess() = %v for source %q", tc.cfg.ContextWindowIsGuess(), tc.want)
			}
		})
	}
}

func TestKnownContextWindow(t *testing.T) {
	cases := []struct {
		model string
		want  int
		known bool
	}{
		{model: "gpt-4o", want: 128000, known: true},
		{model: "DEEPSEEK-CHAT", want: 1048576, known: true},
		{model: "  o3  ", want: 200000, known: true},
		{model: "qwen3:30b", want: 0, known: false},
		{model: "", want: 0, known: false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got, known := KnownContextWindow(tc.model)
			if known != tc.known || (known && got != tc.want) {
				t.Fatalf("KnownContextWindow(%q) = (%d, %v), want (%d, %v)", tc.model, got, known, tc.want, tc.known)
			}
		})
	}
}

func TestSetProviderContextWindow(t *testing.T) {
	newConfig := func(t *testing.T) Config {
		t.Helper()
		cfg := Config{}
		if err := cfg.AddProvider(ProviderConfig{Name: "relay", BaseURL: "https://relay.example.com/v1", Model: "qwen3:30b"}); err != nil {
			t.Fatalf("AddProvider: %v", err)
		}
		if err := cfg.AddProvider(ProviderConfig{Name: "backup", BaseURL: "https://backup.example.com/v1", Model: "m"}); err != nil {
			t.Fatalf("AddProvider: %v", err)
		}
		if err := cfg.SetActiveProvider("relay"); err != nil {
			t.Fatalf("SetActiveProvider: %v", err)
		}
		return cfg
	}

	t.Run("records the window and reports the change", func(t *testing.T) {
		cfg := newConfig(t)
		changed, err := cfg.SetProviderContextWindow("relay", 200000)
		if err != nil {
			t.Fatalf("SetProviderContextWindow: %v", err)
		}
		if !changed || cfg.Provider.ContextWindow != 200000 {
			t.Fatalf("changed = %v, window = %d", changed, cfg.Provider.ContextWindow)
		}
		if cfg.Provider.ContextWindowSource() != ContextWindowSourceConfig {
			t.Fatalf("source = %q", cfg.Provider.ContextWindowSource())
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderContextWindow("relay", 200000); err != nil {
			t.Fatalf("SetProviderContextWindow: %v", err)
		}
		changed, err := cfg.SetProviderContextWindow("relay", 200000)
		if err != nil {
			t.Fatalf("SetProviderContextWindow: %v", err)
		}
		if changed {
			t.Fatal("re-applying the same window reported a change")
		}
	})

	t.Run("zero clears the recorded window", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderContextWindow("relay", 200000); err != nil {
			t.Fatalf("SetProviderContextWindow: %v", err)
		}
		changed, err := cfg.SetProviderContextWindow("relay", 0)
		if err != nil {
			t.Fatalf("SetProviderContextWindow: %v", err)
		}
		if !changed || cfg.Provider.ContextWindow != 0 {
			t.Fatalf("changed = %v, window = %d", changed, cfg.Provider.ContextWindow)
		}
	})

	t.Run("negative is treated as unknown", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderContextWindow("relay", -5); err != nil {
			t.Fatalf("SetProviderContextWindow: %v", err)
		}
		if cfg.Provider.ContextWindow != 0 {
			t.Fatalf("window = %d, want 0", cfg.Provider.ContextWindow)
		}
	})

	t.Run("leaves the active provider alone", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderContextWindow("backup", 32000); err != nil {
			t.Fatalf("SetProviderContextWindow: %v", err)
		}
		if cfg.Provider.Name != "relay" || cfg.Provider.ContextWindow != 0 {
			t.Fatalf("active provider was modified: %+v", cfg.Provider)
		}
		backup, _ := cfg.ProviderByName("backup")
		if backup.ContextWindow != 32000 {
			t.Fatalf("backup window = %d", backup.ContextWindow)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		cfg := newConfig(t)
		if _, err := cfg.SetProviderContextWindow("nope", 1000); err == nil {
			t.Fatal("unknown provider accepted")
		}
	})
}

func TestProviderSummaryShowsTheWindowAndMarksAGuess(t *testing.T) {
	dir := t.TempDir()

	known := ProviderConfig{Name: "relay", BaseURL: "https://relay.example.com/v1", Model: "gpt-4o"}
	if got := known.Summary(dir); !strings.Contains(got, "128k") || strings.Contains(got, "估") {
		t.Fatalf("known window Summary() = %q", got)
	}

	guessed := ProviderConfig{Name: "relay", BaseURL: "https://relay.example.com/v1", Model: "qwen3:30b"}
	if got := guessed.Summary(dir); !strings.Contains(got, "估") {
		t.Fatalf("guessed window Summary() = %q, want it marked", got)
	}

	explicit := ProviderConfig{Name: "relay", BaseURL: "https://relay.example.com/v1", Model: "qwen3:30b", ContextWindow: 200000}
	got := explicit.Summary(dir)
	if !strings.Contains(got, "200k") || strings.Contains(got, "估") {
		t.Fatalf("explicit window Summary() = %q", got)
	}

	echoes := ProviderConfig{Name: "echo", Type: ProviderTypeEcho}
	if got := echoes.Summary(dir); strings.Contains(got, "窗口") {
		t.Fatalf("echo Summary() = %q, want no window segment", got)
	}
}

func TestFormatContextWindow(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{in: 0, want: "0"},
		{in: 999, want: "999"},
		{in: 1000, want: "1k"},
		{in: 8192, want: "8192"},
		{in: 128000, want: "128k"},
		{in: 1048576, want: "1.0M"},
		{in: 2000000, want: "2.0M"},
		{in: -5, want: "0"},
	}
	for _, tc := range cases {
		if got := FormatContextWindow(tc.in); got != tc.want {
			t.Fatalf("FormatContextWindow(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
