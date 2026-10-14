package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"

	"github.com/wislist/mini-opencode/internal/config"
)

// providerCommandResult is what /provider and /model hand back to a front end:
// the text to show, whether the runtime has to be rebuilt, and whether
// the global ~/.mini-opencode/config.json was rewritten.
//
// Both front ends share these handlers so a slash command cannot mean one thing
// in the TUI and another in the line-mode CLI.
type providerCommandResult struct {
	Message string
	Changed bool
	Saved   bool
}

// applyProviderCommand handles /provider for the line-mode CLI:
//
//	/provider                              list the catalog
//	/provider <name>                       switch for this process
//	/provider add <name> <base_url> [model] add or update, and persist
//
// Parsing and state changes live in the config package so the TUI menu, the
// TUI's typed command and this one all behave identically; only the wording
// differs between the two front ends.
func applyProviderCommand(cfg *config.Config, workingDir, input string) providerCommandResult {
	cmd, err := config.ParseProviderCommand(input)
	if err != nil {
		return providerCommandResult{Message: err.Error()}
	}
	if cmd.Kind == config.ProviderCommandAdd && cmd.Name == "" {
		// The line-mode CLI has no overlay, so the interactive path is not
		// available here; say so instead of failing obscurely.
		return providerCommandResult{Message: "usage: /provider add <name> <base_url> [model]" +
			" (run the TUI for interactive setup: enter name, URL, and key, then select fetched models)"}
	}
	previousCatalog := cfg.Providers
	configPath := ""
	if cmd.Kind == config.ProviderCommandAdd {
		configPath, err = config.GlobalConfigPath()
		if err != nil {
			return providerCommandResult{Message: fmt.Sprintf("failed to locate global config.json: %v", err)}
		}
	}
	changed, saved, err := cfg.ApplyProviderCommand(cmd, configPath)
	if err != nil {
		return providerCommandResult{Message: err.Error()}
	}
	switch cmd.Kind {
	case config.ProviderCommandList:
		return providerCommandResult{Message: renderProviderList(*cfg)}
	case config.ProviderCommandAdd:
		return providerCommandResult{
			Message: fmt.Sprintf("wrote provider %s → %s (type %s) to global ~/.mini-opencode/config.json and switched this process\nNext, set api_key or api_key_env in the global config, or enter the key through the TUI /provider add form.",
				cfg.Provider.Name, cfg.Provider.BaseURL, cfg.Provider.EffectiveType()) + updatedNote(previousCatalog, cfg.Provider.Name),
			Changed: changed,
			Saved:   saved,
		}
	default:
		if !changed {
			return providerCommandResult{Message: fmt.Sprintf(
				"provider unchanged: %s (%s)", cfg.Provider.Name, cfg.Provider.Summary())}
		}
		return providerCommandResult{
			Changed: true,
			Message: fmt.Sprintf("switched to provider %s · %s · this process only",
				cfg.Provider.Name, cfg.Provider.Summary()),
		}
	}
}

// updatedNote distinguishes "added" from "updated" in the confirmation, which
// is the only thing that tells the user whether the catalog grew.
func updatedNote(before []config.ProviderConfig, name string) string {
	for _, p := range before {
		if strings.EqualFold(p.Name, name) {
			return " (provider already existed; its entry was updated in place)"
		}
	}
	return ""
}

// renderProviderList prints the catalog with the active entry marked. The rows
// come from config so the CLI listing and the TUI picker describe a provider
// the same way.
func renderProviderList(cfg config.Config) string {
	var b strings.Builder
	b.WriteString("providers:\n")
	for _, p := range cfg.Providers {
		marker := "  "
		if strings.EqualFold(p.Name, cfg.ActiveProvider) {
			marker = "* "
		}
		fmt.Fprintf(&b, "  %s%s\n", marker, p.Summary())
	}
	b.WriteString("  usage: /provider <name> · /provider add <name> <base_url> [model]")
	b.WriteString(" (switches apply to this process only; add writes global ~/.mini-opencode/config.json)")
	return b.String()
}

// applyModelCommand handles /model for the line-mode CLI.
func applyModelCommand(cfg *config.Config, workingDir, input string) providerCommandResult {
	cmd, err := config.ParseModelCommand(input)
	if err != nil {
		return providerCommandResult{Message: err.Error()}
	}
	if cmd.Context {
		path, pathErr := config.GlobalConfigPath()
		if pathErr != nil {
			return providerCommandResult{Message: fmt.Sprintf("failed to locate global config.json: %v", pathErr)}
		}
		changed, message, err := cfg.ApplyModelContext(cmd, path)
		if err != nil {
			return providerCommandResult{Message: err.Error()}
		}
		return providerCommandResult{Changed: changed, Saved: cmd.Window != nil, Message: message}
	}
	if cmd.List {
		if cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
			return providerCommandResult{Message: fmt.Sprintf(
				"provider %q is local echo and has no models to switch", cfg.Provider.Name)}
		}
		return providerCommandResult{Message: renderModelList(*cfg)}
	}
	previous := cfg.Provider.Model
	changed, err := cfg.ApplyModelCommand(cmd)
	if err != nil {
		return providerCommandResult{Message: err.Error()}
	}
	if !changed {
		return providerCommandResult{Message: fmt.Sprintf(
			"model unchanged: %s (provider %s)", cmd.ID, cfg.Provider.Name)}
	}
	_ = previous
	return providerCommandResult{
		Changed: true,
		Message: fmt.Sprintf("switched model to %s (provider %s) · this process only", cmd.ID, cfg.Provider.Name),
	}
}

// modelRefreshTimeout bounds the CLI's model-list request. Long enough for a
// slow relay, short enough that the prompt comes back.
const modelRefreshTimeout = 20 * time.Second

// modelFetcher asks a provider which models it serves, with the metadata the
// endpoint advertises. Injected so the CLI refresh path is testable without a
// server.
type modelFetcher func(ctx context.Context, baseURL, apiKey string) ([]agent.ModelInfo, error)

// applyModelRefresh re-reads the active provider's model list and merges it into
// the configuration.
//
// The line-mode CLI has no overlay, so it takes the union rather than offering a
// checklist: fetching must not drop a model that was configured by hand, and the
// user can still narrow the list by editing the file or running the TUI.
func applyModelRefresh(cfg *config.Config, workingDir string, fetch modelFetcher) providerCommandResult {
	if fetch == nil {
		return providerCommandResult{Message: "model refresh is unavailable because no fetcher is configured"}
	}
	if cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
		return providerCommandResult{Message: fmt.Sprintf(
			"provider %q is local echo and has no model catalog to fetch", cfg.Provider.Name)}
	}
	key := cfg.Provider.ResolvedAPIKey()
	if key == "" {
		return providerCommandResult{Message: fmt.Sprintf(
			"provider %q has no usable API key: set api_key or api_key_env in global ~/.mini-opencode/config.json, or update it through the TUI /provider add form (the third field is the key).",
			cfg.Provider.Name)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), modelRefreshTimeout)
	defer cancel()
	info, err := fetch(ctx, cfg.Provider.BaseURL, key)
	if err != nil {
		return providerCommandResult{Message: fmt.Sprintf("failed to fetch models: %v", err)}
	}
	if len(info) == 0 {
		return providerCommandResult{Message: fmt.Sprintf(
			"%s returned no models; specify one with /model <id>, or list models in global ~/.mini-opencode/config.json.", cfg.Provider.BaseURL)}
	}

	fetched := make([]string, 0, len(info))
	for _, entry := range info {
		fetched = append(fetched, entry.ID)
	}
	merged := config.MergeModels(cfg.Provider.CatalogModels(), fetched)
	next := *cfg
	next.Providers = slices.Clone(cfg.Providers)
	changed, err := next.SetProviderModels(next.Provider.Name, merged, "")
	if err != nil {
		return providerCommandResult{Message: err.Error()}
	}
	// The advertised window is what keeps the ctx indicator honest for a model
	// nothing else knows about. A provider that stays silent leaves whatever the
	// user configured.
	windowNote := ""
	for _, entry := range info {
		if entry.ID != next.Provider.Model || entry.ContextWindow <= 0 {
			continue
		}
		if applied, err := next.SetProviderContextWindow(next.Provider.Name, entry.ContextWindow); err == nil && applied {
			changed = true
			windowNote = fmt.Sprintf(" · window %s (from /models)", config.FormatContextWindow(entry.ContextWindow))
		}
	}
	if !changed {
		return providerCommandResult{Message: fmt.Sprintf(
			"model catalog unchanged (%d): %s", len(merged), strings.Join(merged, ", "))}
	}
	globalPath, err := config.GlobalConfigPath()
	if err != nil {
		return providerCommandResult{Message: fmt.Sprintf("failed to locate global config.json: %v", err)}
	}
	if err := config.SaveProviderCatalog(globalPath, next); err != nil {
		return providerCommandResult{Message: fmt.Sprintf("failed to write global ~/.mini-opencode/config.json: %v", err)}
	}
	*cfg = next
	return providerCommandResult{
		Changed: true,
		Saved:   true,
		Message: fmt.Sprintf("fetched %d models from %s and wrote them to global ~/.mini-opencode/config.json: %s\nCurrent model: %s%s (switch with /model <id>)",
			len(merged), cfg.Provider.BaseURL, strings.Join(merged, ", "), cfg.Provider.Model, windowNote),
	}
}

// renderModelList prints the models /model offers for the active provider.
func renderModelList(cfg config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "models for provider %s:\n", cfg.Provider.Name)
	models := cfg.Provider.CatalogModels()
	if len(models) == 0 {
		b.WriteString("  (no model configured; use /model <id>, or list models in global ~/.mini-opencode/config.json)\n")
	}
	for _, model := range models {
		marker := "  "
		if model == cfg.Provider.Model {
			marker = "* "
		}
		fmt.Fprintf(&b, "  %s%s\n", marker, model)
	}
	b.WriteString("  /model <id> (this process only) · /model context <tokens|1M|auto> (save context window)")
	return b.String()
}

// configureProviderKey stores an API key for the active provider, prompting
// when no key was typed.
//
// With nothing configured yet it creates the built-in DeepSeek entry first, so
// first-run setup stays a single command instead of "edit JSON, then run".
func configureProviderKey(scanner *bufio.Scanner, out io.Writer, workingDir string, cfg *config.Config, key string) error {
	_ = workingDir
	next := *cfg
	next.Providers = slices.Clone(cfg.Providers)
	if next.Provider.EffectiveType() == config.ProviderTypeEcho {
		if !isBareEchoCatalog(next) {
			return fmt.Errorf("provider %q is local echo and does not accept an API key; choose a remote provider with /provider <name>: %s",
				next.Provider.Name, strings.Join(next.ProviderNames(), ", "))
		}
		if err := next.AddProvider(config.DefaultDeepSeekProvider()); err != nil {
			return err
		}
	}
	if key == "" {
		fmt.Fprintf(out, "API key for %s (%s): ", next.Provider.DisplayName(), next.Provider.Name)
		if !scanner.Scan() {
			return scanner.Err()
		}
		key = strings.TrimSpace(scanner.Text())
	}
	if key == "" {
		return fmt.Errorf("api key is required")
	}
	next.Provider.APIKey = key
	for i := range next.Providers {
		if strings.EqualFold(next.Providers[i].Name, next.Provider.Name) {
			next.Providers[i].APIKey = key
			break
		}
	}
	globalPath, err := config.GlobalConfigPath()
	if err != nil {
		return err
	}
	if err := config.SaveProviderCatalog(globalPath, next); err != nil {
		return err
	}
	*cfg = next
	return nil
}

// ensureProviderKey asks for the active provider's key when it is needed and
// missing, so a configured-but-keyless provider fails in a place where the user
// can answer instead of mid-turn.
func ensureProviderKey(scanner *bufio.Scanner, out io.Writer, workingDir string, cfg *config.Config) error {
	if cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
		return nil
	}
	if cfg.Provider.ResolvedAPIKey() != "" {
		return nil
	}
	return configureProviderKey(scanner, out, workingDir, cfg, "")
}

// isBareEchoCatalog reports whether the catalog is nothing but the implicit
// echo fallback, which is what marks a first run.
func isBareEchoCatalog(cfg config.Config) bool {
	return len(cfg.Providers) == 1 && cfg.Providers[0].EffectiveType() == config.ProviderTypeEcho
}
