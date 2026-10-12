package app

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/agent/prompt"
	"github.com/wislist/mini-opencode/internal/agent/tools"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/mcp"
	"github.com/wislist/mini-opencode/internal/memory"
	"github.com/wislist/mini-opencode/internal/session"
	"github.com/wislist/mini-opencode/internal/skills"
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
	cfg, err := config.LoadWorkspace(workingDir)
	if err != nil {
		return err
	}

	model := tui.New(&cfg, workingDir, version)
	memoryStore := memory.NewStore(workingDir)
	memoryRetriever, closeMemoryRetriever := newMemoryRetriever(&cfg, workingDir, memoryStore)
	defer closeMemoryRetriever()

	// Start the configured MCP servers once; their tools are re-registered on
	// every runtime rebuild so provider changes keep them available.
	mcpManager := mcp.NewManager()
	mcpManager.Start(ctx, mcpServerConfigs(cfg.MCPServers))
	defer mcpManager.Close()

	model.SetMCPStatusProvider(func() []string {
		var lines []string
		for _, status := range mcpManager.Statuses() {
			lines = append(lines, status.String())
		}
		return lines
	})

	planHook := agent.NewPlanModeHook(false)
	model.SetPlanHook(planHook)

	sessionStore := session.NewStore(workingDir)
	model.SetSessionStore(sessionStore)
	observer := newSessionFileObserver(sessionStore, model.CurrentSessionID)
	todos := newSessionTodoStore(sessionStore, model.CurrentSessionID)

	model.SetTitleGenerator(func(ctx context.Context, firstUser string) (string, error) {
		generate := makeTitleGenerator(&cfg, workingDir)
		return generate(ctx, firstUser)
	})
	model.SetInitPromptProvider(func() (string, error) {
		return initSystemPrompt(workingDir)
	})

	model.SetSnapshotRestorer(func() (string, error) {
		id := model.CurrentSessionID()
		snap, ok, err := sessionStore.LatestSnapshot(id)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("no snapshot recorded in this session")
		}
		if err := os.WriteFile(snap.Path, snap.Content, 0644); err != nil {
			return "", err
		}
		return snap.Path, nil
	})

	model.SetRuntimeFactory(func(newCfg config.Config) (*agent.Runtime, error) {
		return newTUIRuntime(workingDir, newCfg, model, tuiRuntimeExtras(newCfg, workingDir, mcpManager.Tools(), observer, todos, planHook, model, memoryStore, memoryRetriever))
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

	rt, err := startRuntime(workingDir, &cfg, model, tuiRuntimeExtras(cfg, workingDir, mcpManager.Tools(), observer, todos, planHook, model, memoryStore, memoryRetriever))
	if err != nil {
		return err
	}
	model.SetRuntime(rt)
	// One store serves both the tool and the /memory command, so a note the
	// agent writes is immediately visible to the user.
	model.SetMemoryStore(memoryStore)
	model.SetMemoryRetriever(memoryRetriever)
	// Distillation runs after a run finishes, using whichever provider is
	// configured *then*: resolving per call keeps it from writing notes through
	// a provider the user switched away from.
	model.SetMemoryDistiller(newDistiller(liveProvider{cfg: &cfg, workingDir: workingDir}, memoryStore))
	for _, status := range mcpManager.Statuses() {
		if status.Enabled && status.Err != "" {
			model.AddSystemNotice(fmt.Sprintf("mcp server %q failed: %s", status.Name, status.Err))
		}
	}

	// Mouse wheel handling. Enable Bubble Tea mouse tracking so wheel notches
	// arrive as exact events (Model.handleMouse) instead of dependent on how
	// each terminal translates them. Cell motion rather than all motion keeps
	// drag reporting quiet so text selection still works with the terminal's
	// selection modifier (Option on macOS terminals).
	//
	// xterm alternate-scroll mode stays enabled as a fallback: on terminals
	// that suppress wheel events while a selection modifier is held, the wheel
	// still arrives as cursor up/down keys, which scroll through handleKey.
	output := tui.NewNativeCursorWriter(os.Stdout, model.NativeCursorPosition)
	fmt.Fprint(output, enableAlternateScrollMode)
	defer fmt.Fprint(output, disableAlternateScrollMode)

	p := tea.NewProgram(model, tea.WithOutput(output))
	model.SetProgram(p)

	_, err = p.Run()
	return err
}

// tuiRuntimeExtras assembles the runtime dependencies for the TUI, including
// the subagent runner built from the current provider configuration.
func tuiRuntimeExtras(cfg config.Config, workingDir string, mcpTools []agent.Tool,
	observer tools.FileObserver, todos tools.TodoStore, planHook *agent.PlanModeHook, model *tui.Model,
	memoryStore *memory.Store, memoryRetriever memory.Retriever) runtimeExtras {
	extras := runtimeExtras{
		mcpTools:        mcpTools,
		observer:        observer,
		todos:           todos,
		planHook:        planHook,
		planApprover:    model.MakePlanApprover(),
		todoReader:      todoReaderFor(cfg, todos),
		memories:        memoryStore,
		memoryRetriever: memoryRetriever,
		memoryLimit:     cfg.Memory.EffectiveTopK(),
		// Recall is scored against the session's opening request, so a resumed
		// conversation recalls notes relevant to the work in hand rather than
		// whatever was written most recently.
		memoryQuery: model.MemoryQuery(),
	}
	if subProvider, err := newSubagentProvider(cfg, workingDir); err == nil {
		extras.subagent = newSubagentRunner(subProvider, workingDir, cfg)
	}
	return extras
}

func newTUIRuntime(workingDir string, cfg config.Config, model *tui.Model, extras runtimeExtras) (*agent.Runtime, error) {
	permissions, err := agent.NewModePermissionPolicy(workingDir, cfg.Workspace.AllowedRoots, model.PermissionMode())
	if err != nil {
		return nil, err
	}
	promptContext := prompt.DefaultPromptContext(workingDir)
	contextFiles, err := prompt.DiscoverContextFiles(workingDir, nil)
	if err != nil {
		return nil, err
	}
	promptContext.ContextFiles = contextFiles

	installed, err := skills.LoadSkills(workingDir)
	if err != nil {
		return nil, err
	}
	promptContext.Skills = toPromptSkills(installed)

	systemPrompt, err := prompt.BuildSystemPrompt(prompt.PromptCoder, promptContext)
	if err != nil {
		return nil, err
	}
	recallLimit := extras.memoryLimit
	if recallLimit <= 0 {
		recallLimit = memoryRecallLimit
	}
	if recall := tools.MemoryRecallSectionWithRetriever(context.Background(), extras.memoryRetriever, extras.memoryQuery, recallLimit); recall != "" {
		systemPrompt += "\n\n" + recall
	}

	provider, err := newProvider(cfg.Provider, workingDir)
	if err != nil {
		return nil, err
	}

	summaryPrompt, err := prompt.SummarySystemPrompt(workingDir)
	if err != nil {
		return nil, err
	}

	options := []agent.RuntimeOption{
		agent.WithSystemPrompt(systemPrompt),
		agent.WithMaxTurns(cfg.Agent.EffectiveMaxTurns()),
		agent.WithContextWindow(cfg.Provider.EffectiveContextWindow()),
		agent.WithCompactionPrompt(summaryPrompt),
		agent.WithCompactionThreshold(cfg.Agent.CompactThreshold),
		agent.WithRunRetries(runRetries(cfg)),
		agent.WithTodoReader(extras.todoReader),
		agent.WithPermissionPolicy(permissions),
		agent.WithPermissionConfirmer(model.MakeConfirmer()),
		// The danger guard and loop guard must be registered on the TUI path
		// too, not only in the line-mode CLI: otherwise the interactive UI can
		// run destructive commands the CLI would have blocked.
		agent.WithHook(agent.NewSafetyHook(workingDir)),
		agent.WithHook(agent.NewLoopGuardHook()),
	}
	if extras.planHook != nil {
		options = append(options, agent.WithHook(extras.planHook))
	}

	codingOptions := tools.CodingToolOptions{
		WorkDir:                workingDir,
		FullAccess:             permissions.FullAccess,
		AllowedRoots:           cfg.Workspace.AllowedRoots,
		Observer:               extras.observer,
		RequireReadBeforeWrite: cfg.Workspace.ReadBeforeWrite(),
		Todos:                  extras.todos,
		PlanApprover:           extras.planApprover,
		Web:                    webOptions(cfg, workingDir),
		TaskRunner:             subagentRunnerFor(extras),
	}
	for _, tool := range tools.CodingTools(codingOptions) {
		options = append(options, agent.WithTool(tool))
	}
	if memTool := tools.NewMemoryTool(tools.MemoryOptions{Store: extras.memories, Retriever: extras.memoryRetriever}); memTool != nil {
		options = append(options, agent.WithTool(memTool))
	}
	for _, tool := range extras.mcpTools {
		options = append(options, agent.WithTool(tool))
	}

	return agent.NewRuntime(provider, options...), nil
}

// startRuntime builds the TUI runtime, falling back to the local echo provider
// when the configured one cannot be built.
//
// Missing credentials must not prevent opening the provider settings. Keep the
// original configuration for later repairs, without adding a startup notice.
func startRuntime(workingDir string, cfg *config.Config, model *tui.Model, extras runtimeExtras) (*agent.Runtime, error) {
	rt, err := newTUIRuntime(workingDir, *cfg, model, extras)
	if err == nil {
		return rt, nil
	}
	fallback := *cfg
	fallback.Provider = config.ProviderConfig{Name: config.ProviderTypeEcho, Type: config.ProviderTypeEcho}
	echoRuntime, echoErr := newTUIRuntime(workingDir, fallback, model, extras)
	if echoErr != nil {
		// The provider was not the problem; report the original failure.
		return nil, err
	}
	return echoRuntime, nil
}

// liveProvider resolves the configured provider on every call.
//
// Background work wired once at startup — memory distillation, session titles —
// would otherwise keep talking to whatever provider was configured at launch,
// silently ignoring a later /provider switch.
type liveProvider struct {
	cfg        *config.Config
	workingDir string
}

func (p liveProvider) resolve() (agent.Provider, error) {
	return newProvider(p.cfg.Provider, p.workingDir)
}

func (p liveProvider) Complete(ctx context.Context, req agent.Request) (agent.AssistantResponse, error) {
	provider, err := p.resolve()
	if err != nil {
		return agent.AssistantResponse{}, err
	}
	return provider.Complete(ctx, req)
}

func (p liveProvider) CompleteStream(ctx context.Context, req agent.Request, onDelta func(string)) (agent.AssistantResponse, error) {
	provider, err := p.resolve()
	if err != nil {
		return agent.AssistantResponse{}, err
	}
	return provider.CompleteStream(ctx, req, onDelta)
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
	if err := config.SaveProjectDisplayNames(workingDir, cfg.User, cfg.Assistant); err != nil {
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
