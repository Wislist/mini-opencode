package config

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
)

// Provider implementation types. The type is what selects code; the name is
// free-form so a third-party relay or gateway can be called whatever the user
// calls it.
const (
	ProviderTypeEcho             = "echo"
	ProviderTypeOpenAICompatible = "openai-compatible"
)

// DisplayName returns the label to show in menus and status output, falling
// back to the name so every row has something printable.
func (p ProviderConfig) DisplayName() string {
	if label := strings.TrimSpace(p.Label); label != "" {
		return label
	}
	return p.Name
}

// NormalizedType returns the provider implementation this entry selects.
//
// An explicit type wins, with the aliases people actually write accepted. When
// Type is empty the name decides: "echo" (or a nameless entry with no
// endpoint) is the local echo provider, anything else is an OpenAI-compatible
// endpoint — which is exactly what makes a custom relay name work without a
// type field.
func (p ProviderConfig) NormalizedType() (string, error) {
	switch typ := strings.ToLower(strings.TrimSpace(p.Type)); typ {
	case "":
		name := strings.ToLower(strings.TrimSpace(p.Name))
		base := strings.TrimSpace(p.BaseURL)
		if name == ProviderTypeEcho || (name == "" && base == "") {
			return ProviderTypeEcho, nil
		}
		return ProviderTypeOpenAICompatible, nil
	case ProviderTypeEcho:
		return ProviderTypeEcho, nil
	case ProviderTypeOpenAICompatible, "openai", "openai_compatible":
		return ProviderTypeOpenAICompatible, nil
	default:
		return "", fmt.Errorf("provider %q: unknown type %q (use %q or %q)",
			p.Name, p.Type, ProviderTypeEcho, ProviderTypeOpenAICompatible)
	}
}

// EffectiveType is NormalizedType for call sites that only need a displayable
// value: an unsupported type reads as empty instead of erroring.
func (p ProviderConfig) EffectiveType() string {
	typ, err := p.NormalizedType()
	if err != nil {
		return ""
	}
	return typ
}

// CatalogModels returns the model ids this provider offers, in order, with the
// active model guaranteed to be present: a picker built from this list can
// always show what is currently selected.
func (p ProviderConfig) CatalogModels() []string {
	models := normalizeModelList(p.Models)
	active := strings.TrimSpace(p.Model)
	if active == "" {
		return models
	}
	for _, model := range models {
		if model == active {
			return models
		}
	}
	return append(models, active)
}

// normalized trims the free-text fields and cleans up the model list, so a
// picker built from the catalog has no blank or doubled rows.
func (p ProviderConfig) normalized() ProviderConfig {
	p.Name = strings.TrimSpace(p.Name)
	p.Label = strings.TrimSpace(p.Label)
	p.Type = strings.TrimSpace(p.Type)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	p.Model = strings.TrimSpace(p.Model)
	p.APIKey = strings.TrimSpace(p.APIKey)
	p.APIKeyEnv = strings.TrimSpace(p.APIKeyEnv)
	p.Models = normalizeModelList(p.Models)
	return p
}

// merge fills empty optional fields from an existing entry. Re-adding a
// provider to change one value must not silently drop the rest of its
// configuration.
func (p ProviderConfig) merge(existing ProviderConfig) ProviderConfig {
	if p.Label == "" {
		p.Label = existing.Label
	}
	if p.Type == "" {
		p.Type = existing.Type
	}
	if p.BaseURL == "" {
		p.BaseURL = existing.BaseURL
	}
	if p.Model == "" {
		p.Model = existing.Model
	}
	if p.Models == nil {
		p.Models = existing.Models
	}
	if p.APIKey == "" {
		p.APIKey = existing.APIKey
	}
	if p.APIKeyEnv == "" {
		p.APIKeyEnv = existing.APIKeyEnv
	}
	if p.ContextWindow == 0 {
		p.ContextWindow = existing.ContextWindow
	}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = existing.TimeoutSeconds
	}
	if p.MaxRetries == nil {
		p.MaxRetries = existing.MaxRetries
	}
	return p
}

func normalizeModelList(models []string) []string {
	if len(models) == 0 {
		return nil
	}
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, dup := seen[model]; dup {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ProviderByName looks a catalog entry up by name, case-insensitively, so a
// typed "/provider Relay" works whatever the config's spelling is.
func (c Config) ProviderByName(name string) (ProviderConfig, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ProviderConfig{}, false
	}
	for _, p := range c.Providers {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return ProviderConfig{}, false
}

// ProviderNames returns the catalog names in configuration order, the same
// order the picker shows them in.
func (c Config) ProviderNames() []string {
	names := make([]string, 0, len(c.Providers))
	for _, p := range c.Providers {
		names = append(names, p.Name)
	}
	return names
}

// SetActiveProvider switches which catalog entry Provider resolves to. The
// change is in-memory only: the caller decides whether to persist it.
func (c *Config) SetActiveProvider(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("provider name is required")
	}
	p, ok := c.ProviderByName(name)
	if !ok {
		return fmt.Errorf("unknown provider %q (configured: %s)", name, strings.Join(c.ProviderNames(), ", "))
	}
	c.Provider = p
	c.ActiveProvider = p.Name
	return nil
}

// Validate reports whether this entry is usable as configured.
//
// It runs where the user can still fix the problem — the add form and /provider
// add — rather than at the first request, where "base_url is required" arrives
// long after the moment the field was left empty.
func (p ProviderConfig) Validate() error {
	p = p.normalized()
	if p.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	if strings.ContainsAny(p.Name, " \t") {
		return fmt.Errorf("provider name %q must not contain whitespace", p.Name)
	}
	if _, err := p.NormalizedType(); err != nil {
		return err
	}
	if p.EffectiveType() == ProviderTypeOpenAICompatible && !IsAbsoluteHTTPURL(p.BaseURL) {
		return fmt.Errorf("provider %q: base_url 必须是绝对 http(s) 地址，例如 https://relay.example.com/v1", p.Name)
	}
	return nil
}

// MergeModels returns the union of two model lists: the known order is kept and
// ids only the fetched list has are appended. Fetching must not silently drop a
// model the user typed by hand.
func MergeModels(known, fetched []string) []string {
	merged := make([]string, 0, len(known)+len(fetched))
	merged = append(merged, known...)
	merged = append(merged, fetched...)
	return normalizeModelList(merged)
}

// SetProviderModels records the models a provider serves and keeps its active
// model inside that set: a model the provider no longer offers must not stay
// selected, or every request would fail with a stale id. An empty list keeps
// whatever model was recorded, since there is nothing better to pick.
func (c *Config) SetProviderModels(name string, models []string, preferred string) (bool, error) {
	idx := -1
	for i, p := range c.Providers {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, fmt.Errorf("unknown provider %q (configured: %s)", name, strings.Join(c.ProviderNames(), ", "))
	}

	entry := c.Providers[idx]
	before := entry
	entry.Models = normalizeModelList(models)
	switch {
	case preferred != "" && containsModel(entry.Models, preferred):
		entry.Model = preferred
	case containsModel(entry.Models, entry.Model):
		// Still offered: leave the choice alone.
	case len(entry.Models) > 0:
		entry.Model = entry.Models[0]
	}

	c.Providers[idx] = entry
	if strings.EqualFold(c.Provider.Name, entry.Name) {
		c.Provider = entry
	}
	return !slices.Equal(before.Models, entry.Models) || before.Model != entry.Model, nil
}

func containsModel(models []string, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, m := range models {
		if m == model {
			return true
		}
	}
	return false
}

// AddProvider inserts a provider into the catalog (or updates it in place when
// the name already exists) and makes it active. Fields the caller leaves empty
// are inherited from the existing entry, so this is an update, not an
// overwrite.
func (c *Config) AddProvider(p ProviderConfig) error {
	p = p.normalized()
	for i, existing := range c.Providers {
		if !strings.EqualFold(existing.Name, p.Name) {
			continue
		}
		merged := p.merge(existing)
		// The configured spelling stays canonical: re-adding "RELAY" must not
		// rename the entry users already have in their config.
		merged.Name = existing.Name
		// Validate after the merge: an update that only carries the fields it
		// means to change is the whole point of merge, so checking the incoming
		// entry alone would reject exactly the case merge exists for.
		if err := merged.Validate(); err != nil {
			return err
		}
		c.Providers[i] = merged
		c.Provider = merged
		c.ActiveProvider = merged.Name
		return nil
	}
	if err := p.Validate(); err != nil {
		return err
	}
	c.Providers = append(c.Providers, p)
	c.Provider = p
	c.ActiveProvider = p.Name
	return nil
}

// normalizeProviders turns whatever shape the file used into one catalog plus
// one resolved active provider.
//
// The array supersedes the legacy single block: a config carrying both would
// otherwise have two competing sources of truth, and the one the user just
// wrote would lose.
func (c *Config) normalizeProviders() error {
	catalog := c.Providers
	if len(catalog) == 0 {
		catalog = []ProviderConfig{c.Provider}
	}

	normalized := make([]ProviderConfig, 0, len(catalog))
	seen := make(map[string]struct{}, len(catalog))
	for _, p := range catalog {
		p = p.normalized()
		if p.Name == "" {
			continue
		}
		key := strings.ToLower(p.Name)
		if _, dup := seen[key]; dup {
			continue
		}
		if p.Type != "" {
			if _, err := p.NormalizedType(); err != nil {
				return err
			}
		}
		seen[key] = struct{}{}
		normalized = append(normalized, p)
	}
	if len(normalized) == 0 {
		normalized = []ProviderConfig{{Name: ProviderTypeEcho}}
	}
	c.Providers = normalized

	if active := strings.TrimSpace(c.ActiveProvider); active != "" {
		p, ok := c.ProviderByName(active)
		if !ok {
			return fmt.Errorf("unknown active_provider %q (configured: %s)",
				active, strings.Join(c.ProviderNames(), ", "))
		}
		c.Provider = p
		c.ActiveProvider = p.Name
		return nil
	}
	// Without an explicit choice the first entry wins, which is also how the
	// legacy single-provider config resolves (its block is the whole catalog).
	c.Provider = normalized[0]
	c.ActiveProvider = normalized[0].Name
	return nil
}

// Where EffectiveContextWindow's number comes from.
const (
	// ContextWindowSourceConfig: an explicit context_window in the config.
	ContextWindowSourceConfig = "config"
	// ContextWindowSourceModel: the built-in table knows this model.
	ContextWindowSourceModel = "model"
	// ContextWindowSourceGuess: the model is unknown, so the number is only a
	// placeholder wide enough not to overflow. It must be visible as such: a
	// made-up window is what makes a fresh session report a nearly full context.
	ContextWindowSourceGuess = "guess"
)

// guessContextWindow is the placeholder used for a model nothing is known about.
// Deliberately small rather than generous: compacting too early wastes tokens,
// while overrunning a real window is a hard API error mid-conversation.
const guessContextWindow = 8192

// ContextWindowSource reports whether the effective window is configured, known
// from the built-in table, or only a guess.
func (p ProviderConfig) ContextWindowSource() string {
	if p.ContextWindow > 0 {
		return ContextWindowSourceConfig
	}
	if _, known := KnownContextWindow(p.Model); known {
		return ContextWindowSourceModel
	}
	return ContextWindowSourceGuess
}

// ContextWindowIsGuess reports whether the effective window is only a
// placeholder, which is what the ctx indicator marks.
func (p ProviderConfig) ContextWindowIsGuess() bool {
	return p.ContextWindowSource() == ContextWindowSourceGuess
}

// KnownContextWindow returns the built-in context size for a model, and whether
// the table actually knows it. "Unknown" is reported rather than folded into a
// default so callers can label the fallback instead of presenting it as fact.
func KnownContextWindow(model string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "deepseek-chat", "deepseek-reasoner", "deepseek-coder":
		return 1048576, true
	case "gpt-4o", "gpt-4o-mini":
		return 128000, true
	case "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano":
		return 1047576, true
	case "o3", "o4-mini":
		return 200000, true
	default:
		return 0, false
	}
}

// FormatContextWindow renders a window compactly for menus and status lines.
func FormatContextWindow(tokens int) string {
	if tokens <= 0 {
		return "0"
	}
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 && tokens%1000 == 0 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// SetProviderContextWindow records the context length reported for a provider's
// active model. Zero clears it, which means "unknown" and restores the guess.
func (c *Config) SetProviderContextWindow(name string, window int) (bool, error) {
	if window < 0 {
		window = 0
	}
	idx := -1
	for i, p := range c.Providers {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, fmt.Errorf("unknown provider %q (configured: %s)", name, strings.Join(c.ProviderNames(), ", "))
	}
	entry := c.Providers[idx]
	if entry.ContextWindow == window {
		return false, nil
	}
	entry.ContextWindow = window
	c.Providers[idx] = entry
	if strings.EqualFold(c.Provider.Name, entry.Name) {
		c.Provider = entry
	}
	return true, nil
}

// KeySource reports where a provider's API key comes from, in the same
// precedence the resolution uses: inline config, environment, local secret
// store. It answers "why did switching to this provider fail" without the user
// having to read two files.
func (p ProviderConfig) KeySource(workingDir string) string {
	if p.EffectiveType() == ProviderTypeEcho {
		return "n/a"
	}
	if strings.TrimSpace(p.APIKey) != "" {
		return "config"
	}
	if p.APIKeyEnv != "" && strings.TrimSpace(os.Getenv(p.APIKeyEnv)) != "" {
		return "env:" + p.APIKeyEnv
	}
	if store, err := LoadSecretStore(workingDir); err == nil {
		if strings.TrimSpace(store.ProviderKeys[p.Name]) != "" {
			return "secrets"
		}
	}
	if p.APIKeyEnv != "" {
		return "env:" + p.APIKeyEnv + " (未设置)"
	}
	return "missing"
}

// Summary renders one display row for a provider: display name, endpoint, the
// active model and where its key comes from. Both front ends show the same row
// so a provider looks the same in the CLI listing and in the picker.
func (p ProviderConfig) Summary(workingDir string) string {
	parts := []string{p.DisplayName()}
	if p.Name != p.DisplayName() {
		parts = append(parts, "("+p.Name+")")
	}
	if p.EffectiveType() == ProviderTypeEcho {
		return strings.Join(append(parts, "本地回显，无需密钥"), "  ")
	}
	if p.BaseURL != "" {
		parts = append(parts, p.BaseURL)
	}
	if p.Model != "" {
		parts = append(parts, "模型 "+p.Model)
	}
	window := "窗口 " + FormatContextWindow(p.EffectiveContextWindow())
	if p.ContextWindowIsGuess() {
		// The marker is the difference between "your model really is 8k" and
		// "we do not know, tell us with context_window".
		window += "（估）"
	}
	parts = append(parts, window, "key="+p.KeySource(workingDir))
	return strings.Join(parts, "  ")
}

// ProviderCommandKind is what a /provider invocation asks for.
type ProviderCommandKind string

const (
	ProviderCommandList   ProviderCommandKind = "list"
	ProviderCommandSwitch ProviderCommandKind = "switch"
	ProviderCommandAdd    ProviderCommandKind = "add"
)

// ProviderCommand is a parsed /provider invocation.
type ProviderCommand struct {
	Kind ProviderCommandKind
	// Name is the provider to switch to or to add.
	Name string
	// URL is the endpoint of an added provider.
	URL string
	// Model is the optional default model of an added provider.
	Model string
}

// ModelCommand is a parsed /model invocation.
type ModelCommand struct {
	// List is set when no model id was given.
	List bool
	ID   string
}

// ParseProviderCommand parses a /provider command without touching state, so a
// front end can validate before it mutates anything. The same parser serves the
// CLI and the TUI: a command cannot mean one thing in one front end and
// something else in the other.
func ParseProviderCommand(input string) (ProviderCommand, error) {
	fields := strings.Fields(input)
	if len(fields) < 2 {
		return ProviderCommand{Kind: ProviderCommandList}, nil
	}
	if fields[1] == "add" {
		args := fields[2:]
		if len(args) == 0 {
			// A bare "add" is not an error: it is the interactive path, which
			// the TUI serves with a form and the CLI answers with usage.
			return ProviderCommand{Kind: ProviderCommandAdd}, nil
		}
		if len(args) < 2 || len(args) > 3 {
			return ProviderCommand{}, fmt.Errorf("usage: /provider add <name> <base_url> [model]")
		}
		if !IsAbsoluteHTTPURL(args[1]) {
			return ProviderCommand{}, fmt.Errorf("provider url %q 必须是绝对 http(s) 地址，例如 https://relay.example.com/v1", args[1])
		}
		cmd := ProviderCommand{Kind: ProviderCommandAdd, Name: args[0], URL: args[1]}
		if len(args) == 3 {
			cmd.Model = args[2]
		}
		return cmd, nil
	}
	if len(fields) != 2 {
		return ProviderCommand{}, fmt.Errorf("usage: /provider <name> · /provider add <name> <base_url> [model]")
	}
	return ProviderCommand{Kind: ProviderCommandSwitch, Name: fields[1]}, nil
}

// ParseModelCommand parses a /model command. An unrecognized id is not an
// error: a relay may serve models the config never enumerated, and refusing
// them would break the command exactly where it is most useful.
func ParseModelCommand(input string) (ModelCommand, error) {
	args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(input, "/model")))
	switch len(args) {
	case 0:
		return ModelCommand{List: true}, nil
	case 1:
		return ModelCommand{ID: args[0]}, nil
	default:
		return ModelCommand{}, fmt.Errorf("usage: /model <id>")
	}
}

// ApplyProviderCommand mutates the configuration for a parsed command and
// reports whether the active provider changed (the caller has to rebuild the
// runtime) and whether config.json was rewritten.
//
// Switching stays in memory: the file is the configuration, and a menu choice
// is not. Adding writes it, because "add a provider" is a configuration change
// by definition. A failed write rolls the in-memory catalog back, so memory and
// disk never disagree about which providers exist.
func (c *Config) ApplyProviderCommand(cmd ProviderCommand, configPath string) (changed, saved bool, err error) {
	switch cmd.Kind {
	case ProviderCommandList:
		return false, false, nil
	case ProviderCommandSwitch:
		previous := c.Provider.Name
		if err := c.SetActiveProvider(cmd.Name); err != nil {
			return false, false, err
		}
		return !strings.EqualFold(previous, c.Provider.Name), false, nil
	case ProviderCommandAdd:
		prevProviders, prevActive, prevResolved := c.Providers, c.ActiveProvider, c.Provider
		if err := c.AddProvider(ProviderConfig{Name: cmd.Name, BaseURL: cmd.URL, Model: cmd.Model}); err != nil {
			return false, false, err
		}
		if err := Save(configPath, *c); err != nil {
			c.Providers, c.ActiveProvider, c.Provider = prevProviders, prevActive, prevResolved
			return false, false, fmt.Errorf("写入 %s 失败：%w", configPath, err)
		}
		return true, true, nil
	default:
		return false, false, fmt.Errorf("unknown provider command %q", cmd.Kind)
	}
}

// ApplyModelCommand switches the active provider's model. The catalog entry is
// updated in step, otherwise the next provider switch would restore the model
// recorded in the file and silently undo this choice.
func (c *Config) ApplyModelCommand(cmd ModelCommand) (changed bool, err error) {
	if cmd.List {
		return false, nil
	}
	if c.Provider.EffectiveType() == ProviderTypeEcho {
		return false, fmt.Errorf("provider %q 是本地回显，没有可切换的模型", c.Provider.Name)
	}
	previous := c.Provider.Model
	c.Provider.Model = cmd.ID
	for i := range c.Providers {
		if strings.EqualFold(c.Providers[i].Name, c.Provider.Name) {
			c.Providers[i].Model = cmd.ID
			break
		}
	}
	return previous != cmd.ID, nil
}

// IsAbsoluteHTTPURL reports whether raw is an absolute http(s) URL a provider
// endpoint could actually live at.
func IsAbsoluteHTTPURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}
