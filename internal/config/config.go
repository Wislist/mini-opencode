package config

import (
	"bytes"
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
	ActiveProvider string                     `json:"active_provider,omitempty"`
	MCPServers     map[string]MCPServerConfig `json:"mcpServers"`
	Workspace      WorkspaceConfig            `json:"workspace"`
	Web            WebConfig                  `json:"web"`
	Agent          AgentConfig                `json:"agent"`
	Permissions    PermissionsConfig          `json:"permissions"`
	// User and Assistant are the display names shown in the TUI message
	// labels. Defaults are applied in Load when empty.
	User      string `json:"user,omitempty"`
	Assistant string `json:"assistant,omitempty"`
}

type PermissionsConfig struct {
	Mode agent.PermissionMode `json:"mode"`
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

type SecretStore struct {
	ProviderKeys map[string]string `json:"provider_keys"`
}

func Default() Config {
	return Config{
		Provider:    ProviderConfig{Name: "echo"},
		MCPServers:  map[string]MCPServerConfig{},
		User:        "you",
		Assistant:   "assistant",
		Permissions: PermissionsConfig{Mode: agent.PermissionModeAsk},
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
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
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
	mode, err := agent.ParsePermissionMode(string(cfg.Permissions.Mode))
	if err != nil {
		return cfg, err
	}
	cfg.Permissions.Mode = mode
	if err := cfg.normalizeProviders(); err != nil {
		return cfg, err
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
	return cfg, nil
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
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
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

func (c ProviderConfig) ResolvedAPIKeyFrom(workDir string) string {
	if key := c.ResolvedAPIKey(); key != "" {
		return key
	}
	store, err := LoadSecretStore(workDir)
	if err != nil {
		return ""
	}
	return store.ProviderKeys[c.Name]
}

func LoadSecretStore(workDir string) (SecretStore, error) {
	store := SecretStore{ProviderKeys: map[string]string{}}
	data, err := os.ReadFile(secretStorePath(workDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		return store, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return store, err
	}
	if store.ProviderKeys == nil {
		store.ProviderKeys = map[string]string{}
	}
	return store, nil
}

func SaveProviderKey(workDir string, providerName string, key string) error {
	store, err := LoadSecretStore(workDir)
	if err != nil {
		return err
	}
	if store.ProviderKeys == nil {
		store.ProviderKeys = map[string]string{}
	}
	store.ProviderKeys[providerName] = key

	path := secretStorePath(workDir)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0600)
}

func secretStorePath(workDir string) string {
	return filepath.Join(workDir, ".mini-opencode", "secrets.json")
}
