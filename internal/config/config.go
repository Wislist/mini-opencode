package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"
)

type Config struct {
	// Provider is the resolved active provider. Load keeps it in sync with the
	// Providers entry that ActiveProvider names, so every existing consumer can
	// keep reading a single provider without knowing about the catalog.
	Provider ProviderConfig `json:"provider"`
	// Providers is the catalog the user can switch between: each entry carries
	// its own name, endpoint, model list and key, which is what lets a
	// third-party relay live next to the built-in providers. When it is
	// non-empty it supersedes the legacy single Provider block.
	Providers []ProviderConfig `json:"providers,omitempty"`
	// ActiveProvider names the Providers entry Provider was resolved from. It
	// is persisted so a restart comes back to the provider last written.
	ActiveProvider string `json:"active_provider,omitempty"`
	// ProviderConfigured records whether the global config explicitly contains
	// a provider catalog. It is runtime metadata only: the default echo provider
	// also exists when no config file has ever been written, so the resolved
	// Provider value alone cannot distinguish first-run setup from an explicit
	// choice of the local echo provider.
	ProviderConfigured bool `json:"-"`
	// ProviderConfigurationKnown is false for Config values assembled directly
	// by older callers. That unknown state preserves their historical behavior;
	// only a loader that inspected the global file may declare setup missing.
	ProviderConfigurationKnown bool                       `json:"-"`
	MCPServers                 map[string]MCPServerConfig `json:"mcpServers"`
	Workspace                  WorkspaceConfig            `json:"workspace"`
	Web                        WebConfig                  `json:"web"`
	Agent                      AgentConfig                `json:"agent"`
	Memory                     MemoryConfig               `json:"memory"`
	Permissions                PermissionsConfig          `json:"permissions"`
	// User and Assistant are the display names shown in the TUI message
	// labels. Defaults are applied in Load when empty.
	User      string `json:"user,omitempty"`
	Assistant string `json:"assistant,omitempty"`
}

type PermissionsConfig struct {
	Mode agent.PermissionMode `json:"mode"`
}

// MemoryConfig controls optional semantic retrieval over durable Markdown
// notes. The provider name points into Config.Providers; the chat provider and
// embedding provider may therefore be different services.
type MemoryConfig struct {
	Retrieval         string  `json:"retrieval,omitempty"`
	EmbeddingProvider string  `json:"embedding_provider,omitempty"`
	EmbeddingModel    string  `json:"embedding_model,omitempty"`
	TopK              int     `json:"top_k,omitempty"`
	CandidateK        int     `json:"candidate_k,omitempty"`
	MaxTokens         int     `json:"max_tokens,omitempty"`
	MinSimilarity     float64 `json:"min_similarity,omitempty"`
}

func (c MemoryConfig) HybridEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(c.Retrieval), "hybrid")
}

func (c MemoryConfig) EffectiveTopK() int {
	if c.TopK > 0 {
		return c.TopK
	}
	return 3
}

func (c MemoryConfig) EffectiveCandidateK() int {
	if c.CandidateK > 0 {
		return c.CandidateK
	}
	return 12
}

func (c MemoryConfig) EffectiveMaxTokens() int {
	if c.MaxTokens > 0 {
		return c.MaxTokens
	}
	return 1600
}

func (c MemoryConfig) EffectiveMinSimilarity() float64 {
	if c.MinSimilarity > 0 && c.MinSimilarity <= 1 {
		return c.MinSimilarity
	}
	return 0.35
}

type ProviderConfig struct {
	Name string `json:"name"`
	// Type selects the provider implementation: "echo" or
	// "openai-compatible" (aliases: openai, openai_compatible). Empty infers
	// it from Name: "echo" is the local echo provider, an unnamed provider
	// without an endpoint is echo too, and anything else is treated as an
	// OpenAI-compatible endpoint. That inference is what lets a third-party
	// relay use an arbitrary name.
	Type string `json:"type,omitempty"`
	// Label is the human-facing name shown by /provider and /status. Empty
	// falls back to Name.
	Label string `json:"label,omitempty"`
	// BaseURL is the API root, e.g. "https://relay.example.com/v1".
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	// Models lists the model ids /model offers for this provider. The active
	// Model is always included, so a config that only sets model still gets a
	// picker with one row.
	Models    []string `json:"models,omitempty"`
	APIKey    string   `json:"api_key,omitempty"`
	APIKeyEnv string   `json:"api_key_env"`
	// ContextWindow is the model's max context size in tokens. When zero,
	// EffectiveContextWindow falls back to a model-based default.
	ContextWindow int `json:"context_window,omitempty"`
	// TimeoutSeconds bounds a single provider request. Zero keeps the built-in
	// default (120s).
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// MaxRetries is the retry count after a retryable failure (network error,
	// 408/409/429/5xx). Unset uses the built-in default (2); set 0 explicitly
	// to disable retries.
	MaxRetries *int `json:"max_retries,omitempty"`
}

// RequestTimeout returns the configured per-request timeout, or zero when the
// provider should apply its own default.
func (p ProviderConfig) RequestTimeout() time.Duration {
	if p.TimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(p.TimeoutSeconds) * time.Second
}

// Retries returns the configured retry count and whether it was set at all.
func (p ProviderConfig) Retries() (int, bool) {
	if p.MaxRetries == nil {
		return 0, false
	}
	return *p.MaxRetries, true
}

// WorkspaceConfig controls the agent's filesystem access boundary. By default
// the agent may only touch the working directory it was launched from.
// AllowedRoots lists additional absolute or working-dir-relative paths the
// agent is permitted to read and write, useful when launching from a
// subdirectory while needing access to the whole project.
type WorkspaceConfig struct {
	AllowedRoots []string `json:"allowed_roots"`
	// RequireReadBeforeWrite rejects write/edit on an existing file the agent
	// has not read in the current session. Defaults to true; set false to let
	// the agent overwrite files it never read.
	RequireReadBeforeWrite *bool `json:"require_read_before_write,omitempty"`
}

// ReadBeforeWrite reports whether the read-before-write rule is enabled
// (default true).
func (w WorkspaceConfig) ReadBeforeWrite() bool {
	if w.RequireReadBeforeWrite == nil {
		return true
	}
	return *w.RequireReadBeforeWrite
}

// AgentConfig tunes the agent loop.
type AgentConfig struct {
	// MaxTurns bounds how many provider turns a single run may take. Zero
	// uses the built-in default.
	MaxTurns int `json:"max_turns,omitempty"`
	// CompactThreshold is the fraction of the context window that triggers
	// automatic compaction. Zero uses the built-in default (0.85).
	CompactThreshold float64 `json:"compact_threshold,omitempty"`
	// RunRetries is how many times a failed turn is retried before the run
	// fails. Unset uses the built-in default (2); 0 disables run-level retries
	// (the provider still retries retryable HTTP failures on its own).
	RunRetries *int `json:"run_retries,omitempty"`
	// TodoChain drives the session task list to completion without waiting for
	// the user between items. Defaults to true.
	TodoChain *bool `json:"todo_chain,omitempty"`
}

// Retries returns the configured run-level retry count and whether it was set.
func (a AgentConfig) Retries() (int, bool) {
	if a.RunRetries == nil {
		return 0, false
	}
	return *a.RunRetries, true
}

// TodoChainEnabled reports whether the todo chain is on (default true).
func (a AgentConfig) TodoChainEnabled() bool {
	if a.TodoChain == nil {
		return true
	}
	return *a.TodoChain
}

// DefaultMaxTurns is the turn budget of a single run.
const DefaultMaxTurns = 100

// EffectiveMaxTurns returns the configured turn budget or the default.
func (a AgentConfig) EffectiveMaxTurns() int {
	if a.MaxTurns > 0 {
		return a.MaxTurns
	}
	return DefaultMaxTurns
}

// WebConfig configures the web tools. SearchURL is required for web_search; a
// SearXNG instance with JSON output works, for example
// "http://localhost:8888/search?format=json&q={query}".
type WebConfig struct {
	SearchURL       string `json:"search_url,omitempty"`
	SearchAPIKey    string `json:"search_api_key,omitempty"`
	SearchAPIKeyEnv string `json:"search_api_key_env,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
}

// Timeout returns the configured web request timeout, or zero for the default.
func (w WebConfig) Timeout() time.Duration {
	if w.TimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(w.TimeoutSeconds) * time.Second
}

// MCPServerConfig configures one MCP server.
//
// A stdio server is launched with Command/Args. An http or sse server is
// reached at URL; Headers are sent verbatim, and Token/TokenEnv become an
// "Authorization: Bearer" header unless Headers already carries one.
type MCPServerConfig struct {
	Enabled bool `json:"enabled"`
	// Type is the transport: "stdio" (default), "http" or "sse".
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
	// Headers are extra request headers for a remote server, e.g. a tenant id.
	Headers map[string]string `json:"headers,omitempty"`
	// Token is a static bearer token. Prefer TokenEnv so the secret stays out
	// of config.json.
	Token    string `json:"token,omitempty"`
	TokenEnv string `json:"token_env,omitempty"`
	// TimeoutSeconds bounds one request on a remote transport.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// Timeout returns the configured per-request bound, or zero for the default.
func (c MCPServerConfig) Timeout() time.Duration {
	if c.TimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(c.TimeoutSeconds) * time.Second
}

func Default() Config {
	return Config{
		Provider:                   ProviderConfig{Name: "echo"},
		ProviderConfigured:         true,
		ProviderConfigurationKnown: true,
		MCPServers:                 map[string]MCPServerConfig{},
		User:                       "you",
		Assistant:                  "assistant",
		Permissions:                PermissionsConfig{Mode: agent.PermissionModeAsk},
	}
}

func DefaultDeepSeekProvider() ProviderConfig {
	return ProviderConfig{
		Name:      "deepseek",
		BaseURL:   "https://api.deepseek.com",
		Model:     "deepseek-chat",
		APIKeyEnv: "DEEPSEEK_API_KEY",
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	configured := false
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			return cfg, err
		}
		configured = providerCatalogConfigured(raw)
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cfg, err
		}
	case errors.Is(err, os.ErrNotExist):
		// No file at all: the defaults apply. They still run through the same
		// normalization below, so a caller always receives a resolved provider
		// catalog instead of an empty one it would have to special-case.
	default:
		return cfg, err
	}
	cfg.ProviderConfigured = configured
	cfg.ProviderConfigurationKnown = true
	if err := normalizeConfig(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// NeedsProviderSetup reports the one state that should show first-run UI. An
// unknown source is treated as ready for compatibility with directly-built
// Config values; Load and LoadWorkspace always make the source known.
func (c Config) NeedsProviderSetup() bool {
	return c.ProviderConfigurationKnown && !c.ProviderConfigured
}

func normalizeConfig(cfg *Config) error {
	mode, err := agent.ParsePermissionMode(string(cfg.Permissions.Mode))
	if err != nil {
		return err
	}
	cfg.Permissions.Mode = mode
	if err := cfg.normalizeProviders(); err != nil {
		return err
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = map[string]MCPServerConfig{}
	}
	if cfg.User == "" {
		cfg.User = "you"
	}
	if cfg.Assistant == "" {
		cfg.Assistant = "assistant"
	}
	cfg.Workspace.AllowedRoots = normalizeAllowedRoots(cfg.Workspace.AllowedRoots)
	return nil
}

// normalizeAllowedRoots resolves each root to an absolute, cleaned path and
// drops empties and duplicates, producing a stable list across runs.
func normalizeAllowedRoots(roots []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

func Save(path string, cfg Config) error {
	if err := cfg.normalizeProviders(); err != nil {
		return err
	}
	// Provider is a runtime projection and a legacy input field only. Shadow
	// it when saving so the catalog is the single persisted source of truth.
	type configJSON Config
	data, err := json.MarshalIndent(struct {
		*configJSON
		Provider *ProviderConfig `json:"provider,omitempty"`
	}{configJSON: (*configJSON)(&cfg)}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (c ProviderConfig) ResolvedAPIKey() string {
	if c.APIKey != "" {
		return c.APIKey
	}
	if c.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(c.APIKeyEnv)
}

// EffectiveContextWindow returns the configured context window, or a default
// derived from the model name when ContextWindow is unset.
func (c ProviderConfig) EffectiveContextWindow() int {
	if c.ContextWindow > 0 {
		return c.ContextWindow
	}
	return DefaultContextWindow(c.Model)
}

// DefaultContextWindow returns a best-guess max context size in tokens for
// common models, falling back to the guess placeholder when the model is
// unknown. Callers that need to say which of the two they got use
// KnownContextWindow directly.
func DefaultContextWindow(model string) int {
	if window, known := KnownContextWindow(model); known {
		return window
	}
	return guessContextWindow
}
