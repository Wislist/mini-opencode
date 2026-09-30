package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"

	"github.com/wislist/mini-opencode/internal/config"
)

// providerCommandResult is what /provider and /model hand back to a front end:
// the text to show, whether the runtime has to be rebuilt, and whether
// config.json was rewritten.
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
			"（不带参数的交互式新增请运行 TUI：会依次询问名称 / 地址 / 密钥，再拉取模型勾选）"}
	}
	previousCatalog := cfg.Providers
	changed, saved, err := cfg.ApplyProviderCommand(cmd, filepath.Join(workingDir, "config.json"))
	if err != nil {
		return providerCommandResult{Message: err.Error()}
	}
	switch cmd.Kind {
	case config.ProviderCommandList:
		return providerCommandResult{Message: renderProviderList(*cfg, workingDir)}
	case config.ProviderCommandAdd:
		return providerCommandResult{
			Message: fmt.Sprintf("已写入 config.json：provider %s → %s（type %s）· 本次进程已切换\n接着在 config.json 里补上 api_key / api_key_env，或用 TUI 的 /provider add 表单（第三个框就是密钥，写入 .mini-opencode/secrets.json）。",
				cfg.Provider.Name, cfg.Provider.BaseURL, cfg.Provider.EffectiveType()) + updatedNote(previousCatalog, cfg.Provider.Name),
			Changed: changed,
			Saved:   saved,
		}
	default:
		if !changed {
			return providerCommandResult{Message: fmt.Sprintf(
				"provider 未变：%s（%s）", cfg.Provider.Name, cfg.Provider.Summary(workingDir))}
		}
		return providerCommandResult{
			Changed: true,
			Message: fmt.Sprintf("已切换到 provider %s · %s · 仅本次进程生效",
				cfg.Provider.Name, cfg.Provider.Summary(workingDir)),
		}
	}
}

// updatedNote distinguishes "added" from "updated" in the confirmation, which
// is the only thing that tells the user whether the catalog grew.
func updatedNote(before []config.ProviderConfig, name string) string {
	for _, p := range before {
		if strings.EqualFold(p.Name, name) {
			return "（该 provider 已存在，字段已在原条目上更新）"
		}
	}
	return ""
}

// renderProviderList prints the catalog with the active entry marked. The rows
// come from config so the CLI listing and the TUI picker describe a provider
// the same way.
func renderProviderList(cfg config.Config, workingDir string) string {
	var b strings.Builder
	b.WriteString("providers:\n")
	for _, p := range cfg.Providers {
		marker := "  "
		if strings.EqualFold(p.Name, cfg.ActiveProvider) {
			marker = "* "
		}
		fmt.Fprintf(&b, "  %s%s\n", marker, p.Summary(workingDir))
	}
	b.WriteString("  usage: /provider <name> · /provider add <name> <base_url> [model]")
	b.WriteString("（切换仅本次进程生效；add 会写入 config.json）")
	return b.String()
}

// applyModelCommand handles /model for the line-mode CLI.
func applyModelCommand(cfg *config.Config, input string) providerCommandResult {
	cmd, err := config.ParseModelCommand(input)
	if err != nil {
		return providerCommandResult{Message: err.Error()}
	}
	if cmd.List {
		if cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
			return providerCommandResult{Message: fmt.Sprintf(
				"provider %q 是本地回显，没有可切换的模型", cfg.Provider.Name)}
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
			"模型未变：%s（provider %s）", cmd.ID, cfg.Provider.Name)}
	}
	_ = previous
	return providerCommandResult{
		Changed: true,
		Message: fmt.Sprintf("已切换模型 %s（provider %s）· 仅本次进程生效", cmd.ID, cfg.Provider.Name),
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
		return providerCommandResult{Message: "模型拉取功能不可用（未注入 fetcher）"}
	}
	if cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
		return providerCommandResult{Message: fmt.Sprintf(
			"provider %q 是本地回显，没有模型列表可拉取。", cfg.Provider.Name)}
	}
	key := cfg.Provider.ResolvedAPIKeyFrom(workingDir)
	if key == "" {
		return providerCommandResult{Message: fmt.Sprintf(
			"provider %q 还没有可用的密钥：在 config.json 里填 api_key / api_key_env，或用 TUI 的 /provider add 表单补上（第三个框写密钥）。",
			cfg.Provider.Name)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), modelRefreshTimeout)
	defer cancel()
	info, err := fetch(ctx, cfg.Provider.BaseURL, key)
	if err != nil {
		return providerCommandResult{Message: fmt.Sprintf("拉取模型失败：%v", err)}
	}
	if len(info) == 0 {
		return providerCommandResult{Message: fmt.Sprintf(
			"%s 没有返回任何模型；用 /model <id> 指定，或在 config.json 的 models 里列出。", cfg.Provider.BaseURL)}
	}

	fetched := make([]string, 0, len(info))
	for _, entry := range info {
		fetched = append(fetched, entry.ID)
	}
	merged := config.MergeModels(cfg.Provider.CatalogModels(), fetched)
	changed, err := cfg.SetProviderModels(cfg.Provider.Name, merged, "")
	if err != nil {
		return providerCommandResult{Message: err.Error()}
	}
	// The advertised window is what keeps the ctx indicator honest for a model
	// nothing else knows about. A provider that stays silent leaves whatever the
	// user configured.
	windowNote := ""
	for _, entry := range info {
		if entry.ID != cfg.Provider.Model || entry.ContextWindow <= 0 {
			continue
		}
		if applied, err := cfg.SetProviderContextWindow(cfg.Provider.Name, entry.ContextWindow); err == nil && applied {
			changed = true
			windowNote = fmt.Sprintf(" · 窗口 %s（来自 /models）", config.FormatContextWindow(entry.ContextWindow))
		}
	}
	if !changed {
		return providerCommandResult{Message: fmt.Sprintf(
			"模型列表没有变化（%d 个）：%s", len(merged), strings.Join(merged, ", "))}
	}
	if err := config.Save(filepath.Join(workingDir, "config.json"), *cfg); err != nil {
		return providerCommandResult{Message: fmt.Sprintf("写入 config.json 失败：%v", err)}
	}
	return providerCommandResult{
		Changed: true,
		Saved:   true,
		Message: fmt.Sprintf("已从 %s 获取 %d 个模型并写入 config.json：%s\n当前 model：%s%s（用 /model <id> 切换）",
			cfg.Provider.BaseURL, len(merged), strings.Join(merged, ", "), cfg.Provider.Model, windowNote),
	}
}

// renderModelList prints the models /model offers for the active provider.
func renderModelList(cfg config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "models for provider %s:\n", cfg.Provider.Name)
	models := cfg.Provider.CatalogModels()
	if len(models) == 0 {
		b.WriteString("  (未配置模型；用 /model <id> 指定，或在 config.json 的 models 里列出)\n")
	}
	for _, model := range models {
		marker := "  "
		if model == cfg.Provider.Model {
			marker = "* "
		}
		fmt.Fprintf(&b, "  %s%s\n", marker, model)
	}
	b.WriteString("  /model <id>（仅本次进程生效）")
	return b.String()
}

// configureProviderKey stores an API key for the active provider, prompting
// when no key was typed.
//
// With nothing configured yet it creates the built-in DeepSeek entry first, so
// first-run setup stays a single command instead of "edit JSON, then run".
func configureProviderKey(scanner *bufio.Scanner, out io.Writer, workingDir string, cfg *config.Config, key string) error {
	if cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
		if !isBareEchoCatalog(*cfg) {
			return fmt.Errorf("provider %q 是本地回显，不接收密钥；可用 /provider <name> 选择目标提供商：%s",
				cfg.Provider.Name, strings.Join(cfg.ProviderNames(), ", "))
		}
		if err := cfg.AddProvider(config.DefaultDeepSeekProvider()); err != nil {
			return err
		}
	}
	if key == "" {
		fmt.Fprintf(out, "API key for %s (%s): ", cfg.Provider.DisplayName(), cfg.Provider.Name)
		if !scanner.Scan() {
			return scanner.Err()
		}
		key = strings.TrimSpace(scanner.Text())
	}
	if key == "" {
		return fmt.Errorf("api key is required")
	}
	if err := config.SaveProviderKey(workingDir, cfg.Provider.Name, key); err != nil {
		return err
	}
	// The key now lives in secrets.json, so drop any inline copy: one source of
	// truth, and config.json stays safe to read aloud. Everything else the user
	// configured here (endpoint, model, timeout) is left exactly as it was.
	provider := cfg.Provider
	provider.APIKey = ""
	cfg.Provider = provider
	for i := range cfg.Providers {
		if strings.EqualFold(cfg.Providers[i].Name, provider.Name) {
			cfg.Providers[i].APIKey = ""
			break
		}
	}
	return config.Save(filepath.Join(workingDir, "config.json"), *cfg)
}

// ensureProviderKey asks for the active provider's key when it is needed and
// missing, so a configured-but-keyless provider fails in a place where the user
// can answer instead of mid-turn.
func ensureProviderKey(scanner *bufio.Scanner, out io.Writer, workingDir string, cfg *config.Config) error {
	if cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
		return nil
	}
	if cfg.Provider.ResolvedAPIKeyFrom(workingDir) != "" {
		return nil
	}
	return configureProviderKey(scanner, out, workingDir, cfg, "")
}

// isBareEchoCatalog reports whether the catalog is nothing but the implicit
// echo fallback, which is what marks a first run.
func isBareEchoCatalog(cfg config.Config) bool {
	return len(cfg.Providers) == 1 && cfg.Providers[0].EffectiveType() == config.ProviderTypeEcho
}
