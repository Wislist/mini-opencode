package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/agent/prompt"
	"github.com/wislist/mini-opencode/internal/agent/tools"
	"github.com/wislist/mini-opencode/internal/config"
)

// subagentMaxTurns bounds a delegated investigation so it cannot run away.
const subagentMaxTurns = 24

// subagentRunner implements tools.TaskRunner with a nested runtime that only
// has read-only tools. The subagent never sees the parent conversation, and
// only its final answer returns, which keeps exploratory output out of the
// main context.
type subagentRunner struct {
	provider   agent.Provider
	workingDir string
	cfg        config.Config
}

func newSubagentRunner(provider agent.Provider, workingDir string, cfg config.Config) *subagentRunner {
	if provider == nil {
		return nil
	}
	return &subagentRunner{provider: provider, workingDir: workingDir, cfg: cfg}
}

func (r *subagentRunner) RunTask(ctx context.Context, req tools.TaskRequest) (string, error) {
	system, err := prompt.BuildSystemPrompt(prompt.PromptTask, prompt.DefaultPromptContext(r.workingDir))
	if err != nil {
		return "", err
	}
	options := []agent.RuntimeOption{
		agent.WithSystemPrompt(system),
		agent.WithMaxTurns(subagentMaxTurns),
	}
	for _, tool := range tools.ReadOnlyTools(tools.FileOptions{
		WorkDir:      r.workingDir,
		AllowedRoots: r.cfg.Workspace.AllowedRoots,
	}) {
		options = append(options, agent.WithTool(tool))
	}
	rt := agent.NewRuntime(r.provider, options...)
	if err := rt.Run(ctx, req.Prompt, nil); err != nil {
		return "", err
	}
	return lastAssistantMessage(rt.Messages()), nil
}

// lastAssistantMessage returns the content of the final assistant turn.
func lastAssistantMessage(messages []agent.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == agent.RoleAssistant && strings.TrimSpace(messages[i].Content) != "" {
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}

// webOptions converts the persisted web settings into tool options.
func webOptions(cfg config.Config, workingDir string) tools.WebOptions {
	apiKey := strings.TrimSpace(cfg.Web.SearchAPIKey)
	if apiKey == "" && cfg.Web.SearchAPIKeyEnv != "" {
		apiKey = strings.TrimSpace(os.Getenv(cfg.Web.SearchAPIKeyEnv))
	}
	return tools.WebOptions{
		SearchURL:    strings.TrimSpace(cfg.Web.SearchURL),
		SearchAPIKey: apiKey,
		Timeout:      cfg.Web.Timeout(),
	}
}

// newSubagentProvider builds the provider used by the task tool, reusing the
// main provider configuration.
func newSubagentProvider(cfg config.Config, workingDir string) (agent.Provider, error) {
	provider, err := newProvider(cfg.Provider, workingDir)
	if err != nil {
		return nil, fmt.Errorf("subagent provider: %w", err)
	}
	return provider, nil
}
