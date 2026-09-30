package app

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

// loadConfig writes body as config.json in a temp dir and loads it, so the
// tests exercise the same normalization path the app uses at startup.
func loadConfig(t *testing.T, body string) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0644); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg, dir
}

func testScanner(t *testing.T, input string) *bufio.Scanner {
	t.Helper()
	return bufio.NewScanner(strings.NewReader(input))
}

const twoProviders = `{
  "providers": [
    {"name": "relay", "label": "中转", "base_url": "https://relay.example.com/v1", "model": "gpt-4o", "models": ["gpt-4o", "deepseek-chat"], "api_key": "sk-inline"},
    {"name": "echo", "type": "echo"}
  ],
  "active_provider": "relay"
}`

func TestApplyProviderCommandListsCatalog(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)

	res := applyProviderCommand(&cfg, dir, "/provider")
	if res.Changed || res.Saved {
		t.Fatalf("listing changed state: %+v", res)
	}
	for _, want := range []string{"relay", "中转", "echo", "https://relay.example.com/v1", "gpt-4o"} {
		if !strings.Contains(res.Message, want) {
			t.Fatalf("listing %q should mention %q", res.Message, want)
		}
	}
	// The active entry is marked so the list reads as "where am I".
	if !strings.Contains(res.Message, "*") {
		t.Fatalf("listing should mark the active provider: %q", res.Message)
	}
}

func TestApplyProviderCommandSwitches(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)

	res := applyProviderCommand(&cfg, dir, "/provider echo")
	if !res.Changed {
		t.Fatalf("switching did not report a change: %+v", res)
	}
	if res.Saved {
		t.Fatalf("switching must stay in this process, but config was written: %+v", res)
	}
	if cfg.Provider.Name != "echo" || cfg.ActiveProvider != "echo" {
		t.Fatalf("cfg active = %q / %q, want echo", cfg.Provider.Name, cfg.ActiveProvider)
	}
	if !strings.Contains(res.Message, "echo") {
		t.Fatalf("message should name the new provider: %q", res.Message)
	}
	// Nothing was persisted: reloading the file still selects the old provider.
	reloaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Provider.Name != "relay" {
		t.Fatalf("config.json changed on a switch: active = %q", reloaded.Provider.Name)
	}
}

func TestApplyProviderCommandSwitchIsCaseInsensitiveAndIdempotent(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)

	res := applyProviderCommand(&cfg, dir, "/provider RELAY")
	if res.Changed {
		t.Fatalf("re-selecting the active provider should be a no-op: %+v", res)
	}
	if !strings.Contains(res.Message, "relay") {
		t.Fatalf("message = %q", res.Message)
	}
	if cfg.Provider.Name != "relay" {
		t.Fatalf("canonical name lost: %q", cfg.Provider.Name)
	}
}

func TestApplyProviderCommandUnknownNameListsConfigured(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)

	res := applyProviderCommand(&cfg, dir, "/provider nope")
	if res.Changed {
		t.Fatal("an unknown provider must not change state")
	}
	for _, want := range []string{"nope", "relay", "echo"} {
		if !strings.Contains(res.Message, want) {
			t.Fatalf("error %q should mention %q", res.Message, want)
		}
	}
	if cfg.Provider.Name != "relay" {
		t.Fatalf("active provider changed on a failed switch: %q", cfg.Provider.Name)
	}
}

func TestApplyProviderCommandAddPersistsAndActivates(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)

	res := applyProviderCommand(&cfg, dir, "/provider add backup https://backup.example.com/v1 backup-model")
	if !res.Changed || !res.Saved {
		t.Fatalf("adding should change state and persist: %+v", res)
	}
	if cfg.Provider.Name != "backup" {
		t.Fatalf("added provider is not active: %q", cfg.Provider.Name)
	}
	reloaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	added, ok := reloaded.ProviderByName("backup")
	if !ok {
		t.Fatal("added provider is missing from config.json")
	}
	if added.BaseURL != "https://backup.example.com/v1" || added.Model != "backup-model" {
		t.Fatalf("added provider lost fields: %+v", added)
	}
	if len(reloaded.Providers) != 3 {
		t.Fatalf("catalog has %d entries, want 3", len(reloaded.Providers))
	}
	// It has no key yet, so the message has to point at the next step.
	if !strings.Contains(res.Message, "api_key") {
		t.Fatalf("message should tell the user how to add the key: %q", res.Message)
	}
}

func TestApplyProviderCommandAddUpdatesInPlace(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)

	if res := applyProviderCommand(&cfg, dir, "/provider add relay https://new.example.com/v1"); !res.Changed {
		t.Fatalf("update reported no change: %+v", res)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("catalog grew to %d entries on an update", len(cfg.Providers))
	}
	p, _ := cfg.ProviderByName("relay")
	if p.BaseURL != "https://new.example.com/v1" {
		t.Fatalf("BaseURL = %q, want the new endpoint", p.BaseURL)
	}
	if p.Model != "gpt-4o" || p.Label != "中转" {
		t.Fatalf("update dropped fields it was not given: %+v", p)
	}
	if cfg.Provider.Name != "relay" {
		t.Fatalf("updated provider should become active, got %q", cfg.Provider.Name)
	}
}

func TestApplyProviderCommandAddRejectsBadInput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no args", input: "/provider add", want: "usage"},
		{name: "missing url", input: "/provider add relay", want: "usage"},
		{name: "extra args", input: "/provider add a b c d", want: "usage"},
		{name: "relative url", input: "/provider add relay relay.example.com/v1", want: "url"},
		{name: "no scheme", input: "/provider add relay ftp://relay.example.com", want: "url"},
		{name: "hostless url", input: "/provider add relay https://", want: "url"},
		{name: "switch gets too many args", input: "/provider switch a b", want: "usage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, dir := loadConfig(t, twoProviders)
			before := len(cfg.Providers)
			res := applyProviderCommand(&cfg, dir, tc.input)
			if res.Changed {
				t.Fatalf("rejected input changed state: %+v", res)
			}
			if len(cfg.Providers) != before {
				t.Fatalf("catalog changed on rejected input: %d -> %d", before, len(cfg.Providers))
			}
			if !strings.Contains(strings.ToLower(res.Message), strings.ToLower(tc.want)) {
				t.Fatalf("message %q should contain %q", res.Message, tc.want)
			}
		})
	}
}

func TestApplyModelCommandListsAndSwitches(t *testing.T) {
	cfg, _ := loadConfig(t, twoProviders)

	listing := applyModelCommand(&cfg, "/model")
	if listing.Changed {
		t.Fatalf("listing changed state: %+v", listing)
	}
	for _, want := range []string{"gpt-4o", "deepseek-chat", "relay"} {
		if !strings.Contains(listing.Message, want) {
			t.Fatalf("listing %q should mention %q", listing.Message, want)
		}
	}

	res := applyModelCommand(&cfg, "/model deepseek-chat")
	if !res.Changed {
		t.Fatalf("switching model did not report a change: %+v", res)
	}
	if cfg.Provider.Model != "deepseek-chat" {
		t.Fatalf("Model = %q, want deepseek-chat", cfg.Provider.Model)
	}
	if !strings.Contains(res.Message, "deepseek-chat") {
		t.Fatalf("message = %q", res.Message)
	}
}

// Boundary: an id that is not in the configured list is still accepted, and it
// then shows up in the picker — that is what lets a relay serve a model the
// config never enumerated.
func TestApplyModelCommandAcceptsUnlistedModel(t *testing.T) {
	cfg, _ := loadConfig(t, twoProviders)

	res := applyModelCommand(&cfg, "/model brand-new")
	if !res.Changed {
		t.Fatalf("unlisted model rejected: %+v", res)
	}
	if cfg.Provider.Model != "brand-new" {
		t.Fatalf("Model = %q", cfg.Provider.Model)
	}
	models := cfg.Provider.CatalogModels()
	if models[len(models)-1] != "brand-new" {
		t.Fatalf("CatalogModels() = %v, want brand-new appended", models)
	}
}

func TestApplyModelCommandEchoAndUsage(t *testing.T) {
	cfg, _ := loadConfig(t, `{"provider": {"name": "echo"}}`)

	res := applyModelCommand(&cfg, "/model")
	if res.Changed {
		t.Fatalf("echo listing changed state: %+v", res)
	}
	if !strings.Contains(res.Message, "echo") {
		t.Fatalf("message = %q, want it to name the echo provider", res.Message)
	}

	// A blank id is the bare command, not a usage error: trailing spaces are
	// how a menu-driven flow lands back on the listing.
	blank := applyModelCommand(&cfg, "/model   ")
	if blank.Changed || !strings.Contains(blank.Message, "echo") {
		t.Fatalf("blank id should behave like the bare command, got %+v", blank)
	}

	tooMany := applyModelCommand(&cfg, "/model a b")
	if tooMany.Changed || !strings.Contains(tooMany.Message, "usage") {
		t.Fatalf("two ids should print usage, got %+v", tooMany)
	}
}

func TestNewProviderSupportsCustomRelay(t *testing.T) {
	_, dir := loadConfig(t, "")

	// A third-party relay: arbitrary name, its own endpoint, an inline key and
	// a timeout — no code change required to use it.
	relay := config.ProviderConfig{
		Name:           "my-relay",
		BaseURL:        "https://relay.example.com/v1",
		Model:          "gpt-4o",
		APIKey:         "sk-inline",
		TimeoutSeconds: 30,
	}
	provider, err := newProvider(relay, dir)
	if err != nil {
		t.Fatalf("newProvider(relay): %v", err)
	}
	if _, ok := provider.(*agent.OpenAICompatibleProvider); !ok {
		t.Fatalf("provider = %T, want an OpenAI-compatible provider", provider)
	}

	echo, err := newProvider(config.ProviderConfig{Name: "echo"}, dir)
	if err != nil {
		t.Fatalf("newProvider(echo): %v", err)
	}
	if _, ok := echo.(agent.EchoProvider); !ok {
		t.Fatalf("provider = %T, want EchoProvider", echo)
	}
}

func TestNewProviderErrorsNameTheProvider(t *testing.T) {
	_, dir := loadConfig(t, "")

	cases := []struct {
		name string
		cfg  config.ProviderConfig
		want string
	}{
		{
			name: "missing key",
			cfg:  config.ProviderConfig{Name: "relay", BaseURL: "https://relay.example.com/v1", Model: "m"},
			want: "api key is required",
		},
		{
			name: "missing base url",
			cfg:  config.ProviderConfig{Name: "relay", Model: "m", APIKey: "sk"},
			want: "base_url is required",
		},
		{
			name: "missing model",
			cfg:  config.ProviderConfig{Name: "relay", BaseURL: "https://relay.example.com/v1", APIKey: "sk"},
			want: "model is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newProvider(tc.cfg, dir)
			if err == nil {
				t.Fatal("newProvider = nil error, want a failure")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q should contain %q", err.Error(), tc.want)
			}
			if !strings.Contains(err.Error(), "relay") {
				t.Fatalf("error %q should name the provider so a multi-provider config is debuggable", err.Error())
			}
		})
	}
}

func TestConfigureProviderKeyStoresUnderActiveName(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)

	var out strings.Builder
	if err := configureProviderKey(testScanner(t, ""), &out, dir, &cfg, "sk-new"); err != nil {
		t.Fatalf("configureProviderKey: %v", err)
	}
	store, err := config.LoadSecretStore(dir)
	if err != nil {
		t.Fatalf("LoadSecretStore: %v", err)
	}
	if store.ProviderKeys["relay"] != "sk-new" {
		t.Fatalf("store = %+v, want the key under relay", store.ProviderKeys)
	}
	// The inline key is cleared so secrets.json is the single source, and the
	// endpoint/model the user configured survive untouched.
	reloaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	relay, _ := reloaded.ProviderByName("relay")
	if relay.BaseURL != "https://relay.example.com/v1" || relay.Model != "gpt-4o" {
		t.Fatalf("configureProviderKey rewrote the endpoint: %+v", relay)
	}
	if relay.APIKey != "" {
		t.Fatalf("inline key kept in config.json: %q", relay.APIKey)
	}
}

func TestConfigureProviderKeyPromptsWhenEmpty(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)

	var out strings.Builder
	if err := configureProviderKey(testScanner(t, "sk-from-prompt\n"), &out, dir, &cfg, ""); err != nil {
		t.Fatalf("configureProviderKey: %v", err)
	}
	if !strings.Contains(out.String(), "relay") {
		t.Fatalf("prompt %q should name the provider being configured", out.String())
	}
	store, _ := config.LoadSecretStore(dir)
	if store.ProviderKeys["relay"] != "sk-from-prompt" {
		t.Fatalf("store = %+v", store.ProviderKeys)
	}
}

// Boundary: an empty answer must not silently store an empty key.
func TestConfigureProviderKeyRejectsEmptyAnswer(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)

	var out strings.Builder
	if err := configureProviderKey(testScanner(t, "\n"), &out, dir, &cfg, ""); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, err := config.LoadSecretStore(dir); err != nil {
		t.Fatalf("secret store: %v", err)
	}
}

// With a catalog and an echo active provider there is no key to store: saying so is
// better than writing a key nobody can use.
func TestConfigureProviderKeyRefusesEchoWithAlternatives(t *testing.T) {
	cfg, dir := loadConfig(t, `{
	  "providers": [{"name": "echo", "type": "echo"}, {"name": "relay", "base_url": "https://relay.example.com/v1"}],
	  "active_provider": "echo"
	}`)

	var out strings.Builder
	err := configureProviderKey(testScanner(t, ""), &out, dir, &cfg, "sk-x")
	if err == nil {
		t.Fatal("a key was accepted for the echo provider")
	}
	if !strings.Contains(err.Error(), "relay") {
		t.Fatalf("error %q should point at the configured providers", err.Error())
	}
}

// First run: no providers configured at all, so the key prompt bootstraps the built-in
// DeepSeek entry instead of failing.
func TestConfigureProviderKeyBootstrapsDeepSeek(t *testing.T) {
	cfg, dir := loadConfig(t, "")

	var out strings.Builder
	if err := configureProviderKey(testScanner(t, ""), &out, dir, &cfg, "sk-first-run"); err != nil {
		t.Fatalf("configureProviderKey: %v", err)
	}
	if cfg.Provider.Name != "deepseek" {
		t.Fatalf("active provider = %q, want the bootstrapped deepseek", cfg.Provider.Name)
	}
	if cfg.Provider.BaseURL != "https://api.deepseek.com" || cfg.Provider.Model != "deepseek-chat" {
		t.Fatalf("bootstrap provider = %+v", cfg.Provider)
	}
	store, _ := config.LoadSecretStore(dir)
	if store.ProviderKeys["deepseek"] != "sk-first-run" {
		t.Fatalf("store = %+v", store.ProviderKeys)
	}
	reloaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Provider.Name != "deepseek" {
		t.Fatalf("reloaded active provider = %q", reloaded.Provider.Name)
	}
}

func TestEnsureProviderKey(t *testing.T) {
	t.Run("echo needs nothing", func(t *testing.T) {
		cfg, dir := loadConfig(t, "")
		var out strings.Builder
		if err := ensureProviderKey(testScanner(t, ""), &out, dir, &cfg); err != nil {
			t.Fatalf("ensureProviderKey: %v", err)
		}
	})

	t.Run("resolved key needs nothing", func(t *testing.T) {
		cfg, dir := loadConfig(t, twoProviders)
		var out strings.Builder
		if err := ensureProviderKey(testScanner(t, ""), &out, dir, &cfg); err != nil {
			t.Fatalf("ensureProviderKey: %v", err)
		}
	})

	t.Run("missing key is prompted and stored", func(t *testing.T) {
		cfg, dir := loadConfig(t, `{
		  "providers": [{"name": "relay", "base_url": "https://relay.example.com/v1", "model": "m"}]
		}`)
		var out strings.Builder
		if err := ensureProviderKey(testScanner(t, "sk-prompted\n"), &out, dir, &cfg); err != nil {
			t.Fatalf("ensureProviderKey: %v", err)
		}
		store, _ := config.LoadSecretStore(dir)
		if store.ProviderKeys["relay"] != "sk-prompted" {
			t.Fatalf("store = %+v", store.ProviderKeys)
		}
	})
}

// info builds fetched entries from ids, with no window reported.
func info(ids ...string) []agent.ModelInfo {
	entries := make([]agent.ModelInfo, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, agent.ModelInfo{ID: id})
	}
	return entries
}

// modelFetcherStub stands in for the provider request so the CLI refresh path is
// testable without a server.
type modelFetcherStub struct {
	calls  int
	models []agent.ModelInfo
	err    error
}

func (s *modelFetcherStub) fetch(ctx context.Context, baseURL, apiKey string) ([]agent.ModelInfo, error) {
	s.calls++
	_ = ctx
	_ = baseURL
	_ = apiKey
	return s.models, s.err
}

func TestApplyModelRefresh(t *testing.T) {
	t.Run("merges fetched models and persists them", func(t *testing.T) {
		cfg, dir := loadConfig(t, twoProviders)
		stub := &modelFetcherStub{models: info("gpt-4o", "brand-new")}

		res := applyModelRefresh(&cfg, dir, stub.fetch)
		if !res.Changed || !res.Saved {
			t.Fatalf("refresh result = %+v, want changed and saved", res)
		}
		if stub.calls != 1 {
			t.Fatalf("fetch calls = %d, want 1", stub.calls)
		}
		for _, want := range []string{"brand-new", "gpt-4o"} {
			if !strings.Contains(res.Message, want) {
				t.Fatalf("message %q should mention %q", res.Message, want)
			}
		}
		reloaded, err := config.Load(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got := strings.Join(reloaded.Provider.CatalogModels(), ","); got != "gpt-4o,deepseek-chat,brand-new" {
			t.Fatalf("persisted models = %q", got)
		}
		if reloaded.Provider.Model != "gpt-4o" {
			t.Fatalf("active model = %q, want the still-offered gpt-4o kept", reloaded.Provider.Model)
		}
	})

	t.Run("echo has nothing to fetch", func(t *testing.T) {
		cfg, dir := loadConfig(t, `{"provider": {"name": "echo"}}`)
		stub := &modelFetcherStub{models: info("a")}

		res := applyModelRefresh(&cfg, dir, stub.fetch)
		if res.Changed || stub.calls != 0 {
			t.Fatalf("result = %+v, calls = %d", res, stub.calls)
		}
		if !strings.Contains(res.Message, "echo") {
			t.Fatalf("message = %q", res.Message)
		}
	})

	t.Run("missing key points at the key setting", func(t *testing.T) {
		cfg, dir := loadConfig(t, `{
		  "providers": [{"name": "relay", "base_url": "https://relay.example.com/v1", "model": "m"}]
		}`)
		stub := &modelFetcherStub{models: info("a")}

		res := applyModelRefresh(&cfg, dir, stub.fetch)
		if res.Changed || stub.calls != 0 {
			t.Fatalf("result = %+v, calls = %d", res, stub.calls)
		}
		if !strings.Contains(res.Message, "api_key") {
			t.Fatalf("message %q should point at the key setting", res.Message)
		}
	})

	t.Run("fetch failure leaves the config alone", func(t *testing.T) {
		cfg, dir := loadConfig(t, twoProviders)
		before := strings.Join(cfg.Provider.CatalogModels(), ",")
		stub := &modelFetcherStub{err: errors.New("provider returned status 500")}

		res := applyModelRefresh(&cfg, dir, stub.fetch)
		if res.Changed || res.Saved {
			t.Fatalf("failed refresh reported a change: %+v", res)
		}
		if !strings.Contains(res.Message, "500") {
			t.Fatalf("message %q should carry the failure", res.Message)
		}
		if got := strings.Join(cfg.Provider.CatalogModels(), ","); got != before {
			t.Fatalf("models changed on failure: %q -> %q", before, got)
		}
	})

	t.Run("empty list is reported and nothing is written", func(t *testing.T) {
		cfg, dir := loadConfig(t, twoProviders)
		stub := &modelFetcherStub{}

		res := applyModelRefresh(&cfg, dir, stub.fetch)
		if res.Changed || res.Saved {
			t.Fatalf("empty refresh reported a change: %+v", res)
		}
		if !strings.Contains(res.Message, "没有返回") {
			t.Fatalf("message = %q", res.Message)
		}
	})

	t.Run("nil fetcher is refused", func(t *testing.T) {
		cfg, dir := loadConfig(t, twoProviders)
		res := applyModelRefresh(&cfg, dir, nil)
		if res.Changed || !strings.Contains(res.Message, "不可用") {
			t.Fatalf("result = %+v", res)
		}
	})
}

// 拉取到的窗口必须落到配置里：否则自定义模型一直按 8k 猜，ctx 一开机就 57%。
func TestApplyModelRefreshStoresTheReportedWindow(t *testing.T) {
	cfg, dir := loadConfig(t, twoProviders)
	stub := &modelFetcherStub{models: []agent.ModelInfo{
		{ID: "gpt-4o", ContextWindow: 131072},
		{ID: "deepseek-chat", ContextWindow: 65536},
	}}

	res := applyModelRefresh(&cfg, dir, stub.fetch)
	if !res.Saved {
		t.Fatalf("refresh result = %+v", res)
	}
	if cfg.Provider.ContextWindow != 131072 {
		t.Fatalf("context_window = %d, want the window reported for the active model", cfg.Provider.ContextWindow)
	}
	if cfg.Provider.ContextWindowSource() != config.ContextWindowSourceConfig {
		t.Fatalf("source = %q", cfg.Provider.ContextWindowSource())
	}
	if !strings.Contains(res.Message, "窗口") {
		t.Fatalf("message should mention the window: %q", res.Message)
	}
	reloaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Provider.ContextWindow != 131072 {
		t.Fatalf("persisted context_window = %d", reloaded.Provider.ContextWindow)
	}
}

// 上报里没有窗口（或没有当前模型）时，不能把已配置的窗口清掉。
func TestApplyModelRefreshKeepsAnUnreportedWindow(t *testing.T) {
	cfg, dir := loadConfig(t, `{
	  "providers": [{"name": "relay", "base_url": "https://relay.example.com/v1", "model": "qwen3:30b", "api_key": "sk", "context_window": 262144}],
	  "active_provider": "relay"
	}`)
	stub := &modelFetcherStub{models: []agent.ModelInfo{
		{ID: "qwen3:30b"},                  // no window advertised
		{ID: "other", ContextWindow: 4096}, // a window, but not the active model
	}}

	if res := applyModelRefresh(&cfg, dir, stub.fetch); !res.Saved {
		t.Fatalf("refresh result = %+v", res)
	}
	if cfg.Provider.ContextWindow != 262144 {
		t.Fatalf("context_window = %d, want the configured 262144 kept", cfg.Provider.ContextWindow)
	}
}

func TestPrintStatusMarksAGuessedWindow(t *testing.T) {
	t.Run("guessed", func(t *testing.T) {
		cfg, dir := loadConfig(t, `{
		  "providers": [{"name": "relay", "base_url": "https://relay.example.com/v1", "model": "qwen3:30b", "api_key": "sk"}],
		  "active_provider": "relay"
		}`)
		var out strings.Builder
		printStatus(&out, statusInput{workingDir: dir, cfg: cfg})
		got := out.String()
		for _, want := range []string{"估值", "context_window"} {
			if !strings.Contains(got, want) {
				t.Fatalf("status %q should mention %q", got, want)
			}
		}
	})

	t.Run("known model is not marked", func(t *testing.T) {
		cfg, dir := loadConfig(t, twoProviders)
		var out strings.Builder
		printStatus(&out, statusInput{workingDir: dir, cfg: cfg})
		if got := out.String(); strings.Contains(got, "估值") {
			t.Fatalf("status marked a known model as a guess: %q", got)
		}
	})
}

// echo 不发请求，为它报「窗口是估值」是噪音：那条提示说的是一个不存在的模型。
func TestPrintStatusSkipsTheWindowForEcho(t *testing.T) {
	cfg, dir := loadConfig(t, "")
	var out strings.Builder
	printStatus(&out, statusInput{workingDir: dir, cfg: cfg})
	got := out.String()
	for _, unwanted := range []string{"估值", "window:"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("echo status %q should not mention %q", got, unwanted)
		}
	}
}
