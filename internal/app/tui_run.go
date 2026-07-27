package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/agent/prompt"
	"github.com/wislist/mini-opencode/internal/agent/tools"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/session"
	"github.com/wislist/mini-opencode/internal/tui"
)

const (
	// Xterm alternate-scroll mode. In the alternate screen, wheel events are sent
	// as cursor up/down keys instead of scrolling the terminal's main scrollback.
	enableAlternateScrollMode  = "\x1b[?1007h"
	disableAlternateScrollMode = "\x1b[?1007l"
)

// RunTUI launches the Bubble Tea full-screen interface.
func RunTUI(ctx context.Context) error {
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.Load("config.json")
	if err != nil {
		return err
	}

	model := tui.New(&cfg, workingDir, version)

	planHook := &agent.PlanModeHook{Active: false}
	model.SetPlanHook(planHook)

	model.SetSessionStore(session.NewStore(workingDir))

	model.SetKeySaver(func(key string) (config.Config, error) {
		return saveProviderKey(workingDir, &cfg, key)
	})
	model.SetNameSaver(func(user, assistant string) (config.Config, error) {
		return saveDisplayNames(workingDir, &cfg, user, assistant)
	})
	model.SetRuntimeFactory(func(newCfg config.Config) (*agent.Runtime, error) {
		return newTUIRuntime(workingDir, newCfg, model)
	})
	model.SetCompactor(func(ctx context.Context) (agent.CompactResult, error) {
		summaryPrompt, err := prompt.SummarySystemPrompt(workingDir)
		if err != nil {
			return agent.CompactResult{}, err
		}
		if model.Runtime() == nil {
			return agent.CompactResult{}, fmt.Errorf("no runtime available")
		}
		return model.Runtime().CompactDetailed(ctx, summaryPrompt)
	})

	rt, err := newTUIRuntime(workingDir, cfg, model)
	if err != nil {
		return err
	}
	model.SetRuntime(rt)

	// Keep the UI in the alternate screen, but do not enable Bubble Tea mouse
	// tracking. Mouse tracking makes many terminals send drag events to the app
	// instead of selecting text, which prevents users from copying generated
	// output.
	//
	// Enabling xterm alternate-scroll mode asks supported terminals to translate
	// the mouse wheel into cursor-key events while the alternate screen is active.
	// That keeps wheel scrolling inside the TUI viewport instead of exposing the
	// terminal scrollback from previous sessions, without stealing drag selection.
	output := tui.NewNativeCursorWriter(os.Stdout, model.NativeCursorPosition)
	fmt.Fprint(output, enableAlternateScrollMode)
	defer fmt.Fprint(output, disableAlternateScrollMode)

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithOutput(output))
	model.SetProgram(p)

	_, err = p.Run()
	return err
}

func newTUIRuntime(workingDir string, cfg config.Config, model *tui.Model) (*agent.Runtime, error) {
	promptContext := prompt.DefaultPromptContext(workingDir)
	contextFiles, err := prompt.DiscoverContextFiles(workingDir, nil)
	if err != nil {
		return nil, err
	}
	promptContext.ContextFiles = contextFiles

	systemPrompt, err := prompt.BuildSystemPrompt(prompt.PromptCoder, promptContext)
	if err != nil {
		return nil, err
	}

	provider, err := newProvider(cfg.Provider, workingDir)
	if err != nil {
		return nil, err
	}

	options := []agent.RuntimeOption{
		agent.WithSystemPrompt(systemPrompt),
		agent.WithPermissionPolicy(agent.NewDefaultPermissionPolicyWithRoots(workingDir, cfg.Workspace.AllowedRoots)),
		agent.WithPermissionConfirmer(model.MakeConfirmer()),
		agent.WithHook(model.PlanHook()),
	}
	for _, tool := range tools.CodingTools(tools.CodingToolOptions{WorkDir: workingDir, AllowedRoots: cfg.Workspace.AllowedRoots}) {
		options = append(options, agent.WithTool(tool))
	}

	return agent.NewRuntime(provider, options...), nil
}

func saveProviderKey(workingDir string, cfg *config.Config, key string) (config.Config, error) {
	provider := cfg.Provider
	if strings.ToLower(provider.Name) != "deepseek" {
		provider = config.DefaultDeepSeekProvider()
	}
	if provider.BaseURL == "" {
		provider.BaseURL = "https://api.deepseek.com"
	}
	if provider.Model == "" {
		provider.Model = "deepseek-chat"
	}
	if provider.APIKeyEnv == "" {
		provider.APIKeyEnv = "DEEPSEEK_API_KEY"
	}
	provider.APIKey = ""
	cfg.Provider = provider
	if err := config.SaveProviderKey(workingDir, provider.Name, key); err != nil {
		return *cfg, err
	}
	if err := config.Save(filepath.Join(workingDir, "config.json"), *cfg); err != nil {
		return *cfg, err
	}
	return *cfg, nil
}

// saveDisplayNames persists custom user/assistant display names to the
// config file. Empty values keep the existing name.
func saveDisplayNames(workingDir string, cfg *config.Config, user, assistant string) (config.Config, error) {
	if user != "" {
		cfg.User = user
	}
	if assistant != "" {
		cfg.Assistant = assistant
	}
	if err := config.Save(filepath.Join(workingDir, "config.json"), *cfg); err != nil {
		return *cfg, err
	}
	return *cfg, nil
}

func IsTerminal(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}
