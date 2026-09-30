package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/tui"
)

// Every field a remote MCP server needs has to survive the config → launch
// mapping; a dropped token or type would show up as an unexplained failed
// server at startup.
func TestMCPServerConfigsMapsTransportsAndAuth(t *testing.T) {
	t.Setenv("MINI_OPENCODE_TEST_MCP_TOKEN", "env-token")

	servers := map[string]config.MCPServerConfig{
		"local": {Enabled: true, Command: "gopls", Args: []string{"mcp"}},
		"github": {
			Enabled:        true,
			Type:           "http",
			URL:            "https://api.github.com/mcp/",
			Headers:        map[string]string{"X-Tenant": "acme"},
			TokenEnv:       "MINI_OPENCODE_TEST_MCP_TOKEN",
			TimeoutSeconds: 10,
		},
		"stream": {Enabled: true, Type: "sse", URL: "https://example.com/sse", Token: "static"},
		"off":    {Enabled: false, Type: "http", URL: "https://example.com/mcp"},
	}

	out := mcpServerConfigs(servers)
	if len(out) != len(servers) {
		t.Fatalf("mapped %d servers, want %d", len(out), len(servers))
	}
	if got := out["local"]; got.Transport() != "stdio" || got.Command != "gopls" || len(got.Args) != 1 {
		t.Fatalf("stdio mapping = %+v", got)
	}
	gh := out["github"]
	if gh.Transport() != "http" || gh.URL != "https://api.github.com/mcp/" {
		t.Fatalf("http mapping = %+v", gh)
	}
	if gh.Timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", gh.Timeout)
	}
	headers := gh.AuthHeaders()
	if headers["X-Tenant"] != "acme" {
		t.Fatalf("custom header lost: %v", headers)
	}
	if headers["Authorization"] != "Bearer env-token" {
		t.Fatalf("token env not resolved: %v", headers)
	}
	if got := out["stream"]; got.Transport() != "sse" || got.AuthHeaders()["Authorization"] != "Bearer static" {
		t.Fatalf("sse mapping = %+v", got)
	}
	if got := out["off"]; got.Enabled {
		t.Fatalf("disabled server mapped as enabled: %+v", got)
	}
}

func TestMCPServerConfigsEdgeCases(t *testing.T) {
	if got := mcpServerConfigs(nil); len(got) != 0 {
		t.Fatalf("nil map produced %d entries", len(got))
	}
	if got := mcpServerConfigs(map[string]config.MCPServerConfig{}); len(got) != 0 {
		t.Fatalf("empty map produced %d entries", len(got))
	}
}

// A configured provider without a key must not lock the user out of the TUI:
// the interface opens on echo and the notice names the repair command.
func TestStartRuntimeFallsBackToEchoWithoutKey(t *testing.T) {
	cfg, dir := loadConfig(t, `{
	  "providers": [{"name": "relay", "base_url": "https://relay.example.com/v1", "model": "gpt-4o"}]
	}`)
	model := tui.New(&cfg, dir, "test")

	rt, notice, err := startRuntime(dir, &cfg, model, runtimeExtras{})
	if err != nil {
		t.Fatalf("startRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("no runtime returned")
	}
	for _, want := range []string{"relay", "api key is required", "/provider add"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice %q should mention %q", notice, want)
		}
	}
	// The configuration still points at the provider the user chose, so the
	// follow-up /key stores the key under the right name.
	if cfg.Provider.Name != "relay" {
		t.Fatalf("startup fallback rewrote the config: %q", cfg.Provider.Name)
	}
}

func TestStartRuntimeUsesTheConfiguredProvider(t *testing.T) {
	cfg, dir := loadConfig(t, `{
	  "providers": [{"name": "relay", "base_url": "https://relay.example.com/v1", "model": "gpt-4o", "api_key": "sk-inline"}]
	}`)
	model := tui.New(&cfg, dir, "test")

	rt, notice, err := startRuntime(dir, &cfg, model, runtimeExtras{})
	if err != nil {
		t.Fatalf("startRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("no runtime returned")
	}
	if notice != "" {
		t.Fatalf("unexpected notice: %q", notice)
	}
}

// The provider is resolved per call, so background work follows a switch.
func TestLiveProviderFollowsTheConfig(t *testing.T) {
	cfg, dir := loadConfig(t, "")
	live := liveProvider{cfg: &cfg, workingDir: dir}

	// echo resolves and answers.
	if _, err := live.Complete(t.Context(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hello"}}}); err != nil {
		t.Fatalf("echo Complete: %v", err)
	}

	// A provider that cannot be built reports the failure instead of silently
	// using the previous one.
	if err := cfg.AddProvider(config.ProviderConfig{
		Name: "relay", BaseURL: "https://relay.example.com/v1", Model: "m",
	}); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	if _, err := live.Complete(t.Context(), agent.Request{Messages: []agent.Message{{Role: agent.RoleUser, Content: "hello"}}}); err == nil {
		t.Fatal("missing key not reported")
	}
}

// A config.json that is not writable must fail loudly rather than pretend the
// provider was added.
func TestApplyProviderCommandReportsWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	cfg, dir := loadConfig(t, twoProviders)
	// Force the write to fail by pointing at a path whose parent is a file.
	blocked := filepath.Join(dir, "config.json", "nested")
	res := applyProviderCommand(&cfg, blocked, "/provider add backup https://backup.example.com/v1")
	if res.Changed || res.Saved {
		t.Fatalf("failed write reported as success: %+v", res)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("catalog kept the rejected provider: %+v", cfg.Providers)
	}
}
