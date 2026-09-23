package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/agent/prompt"
	"github.com/wislist/mini-opencode/internal/agent/tools"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/mcp"
	"github.com/wislist/mini-opencode/internal/memory"
	"github.com/wislist/mini-opencode/internal/session"
	"github.com/wislist/mini-opencode/internal/skills"
)

// version is the fallback build version. A plain `go build` reports this, so the
// tree always has a meaningful version even without the Makefile. `make build`
// overrides it with a git-derived value (see the Makefile), which is why the
// binary — not the source — carries the commit-specific version.
var version = "0.4.0"

// SetVersion overrides the reported version. The main package calls it from
// linker-injected values; tests may call it directly.
func SetVersion(v string) {
	if strings.TrimSpace(v) != "" {
		version = v
	}
}

func Run(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.Load("config.json")
	if err != nil {
		return err
	}
	if err := ensureProviderKey(scanner, out, workingDir, &cfg); err != nil {
		return err
	}

	// The session store and file observer must exist before the runtime is
	// built: the coding tools need the observer to record reads and snapshots.
	sessions := session.NewStore(workingDir)
	currentSession := sessions.Create("new session")
	observer := newSessionFileObserver(sessions, func() string { return currentSession.ID })
	todos := newSessionTodoStore(sessions, func() string { return currentSession.ID })
	planHook := agent.NewPlanModeHook(false)
	planApprover := cliPlanApprover(scanner, out, planHook)
	sessionAllowed := map[string]bool{}
	permissions, err := agent.NewModePermissionPolicy(workingDir, cfg.Workspace.AllowedRoots, cfg.Permissions.Mode)
	if err != nil {
		return err
	}

	// Start configured MCP servers once and reuse their tools across runtime
	// rebuilds. A failing server is reported but never blocks startup.
	mcpManager := mcp.NewManager()
	mcpManager.Start(ctx, mcpServerConfigs(cfg.MCPServers))
	defer mcpManager.Close()
	reportMCPFailures(out, mcpManager)

	extras := runtimeExtras{
		permissions:  permissions,
		mcpTools:     mcpManager.Tools(),
		observer:     observer,
		todos:        todos,
		planHook:     planHook,
		planApprover: planApprover,
		allowedTools: sessionAllowed,
		todoReader:   todoReaderFor(cfg, todos),
		memories:     memory.NewStore(workingDir),
	}
	if subProvider, perr := newSubagentProvider(cfg, workingDir); perr == nil {
		extras.subagent = newSubagentRunner(subProvider, workingDir, cfg)
	}
	runtime, err := newRuntime(workingDir, cfg, scanner, out, extras)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "mini-opencode %s\n", version)
	fmt.Fprintln(out, "commands: /help /version /tools /workspace /status /permissions /skills /mcp /plan /init /fork /undo /key /compact /session /newsession /archive /quit")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		fmt.Fprint(out, "\n> ")
		if !scanner.Scan() {
			return scanner.Err()
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "/permissions" || strings.HasPrefix(input, "/permissions ") {
			mode, changed, message := agent.PermissionCommand(permissions.Mode(), input)
			if changed {
				if err := runtime.SetPermissionMode(mode); err != nil {
					fmt.Fprintf(out, "error: %v\n", err)
					continue
				}
				clear(sessionAllowed)
			}
			fmt.Fprintln(out, message)
			continue
		}
		// /memory takes an optional search query, so it is matched by prefix
		// before the exact-match switch below.
		if input == "/memory" || strings.HasPrefix(input, "/memory ") {
			printMemories(out, workingDir, strings.TrimSpace(strings.TrimPrefix(input, "/memory")))
			fmt.Fprint(out, "\n> ")
			continue
		}

		switch input {
		case "/help":
			printHelp(out)
		case "/version":
			fmt.Fprintf(out, "mini-opencode %s\n", version)
		case "/tools":
			for _, tool := range runtime.Tools() {
				fmt.Fprintf(out, "%s\t%s\n", tool.Name, tool.Description)
			}
		case "/workspace":
			printWorkspace(out, workingDir, cfg.Workspace.AllowedRoots)
		case "/skills":
			printSkills(out, workingDir)
		case "/status":
			statusCfg := cfg
			statusCfg.Permissions.Mode = permissions.Mode()
			printStatus(out, statusInput{
				workingDir: workingDir,
				cfg:        statusCfg,
				runtime:    runtime,
				session:    currentSession,
				planMode:   planHook.IsActive(),
				allowed:    sessionAllowed,
			})
		case "/mcp":
			printMCPStatus(out, mcpManager)
		case "/init":
			system, err := initSystemPrompt(workingDir)
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			previous := runtime.SystemPrompt()
			runtime.SetSystemPrompt(system)
			runErr := runtime.Run(ctx, initUserPrompt, renderEvent(out))
			runtime.SetSystemPrompt(previous)
			if runErr != nil {
				fmt.Fprintf(out, "error: %v\n", runErr)
			}
			_ = saveSession(sessions, currentSession, runtime)
		case "/fork":
			if err := saveSession(sessions, currentSession, runtime); err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			fork, err := sessions.Fork(currentSession.ID, "")
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			currentSession = fork
			runtime.SetMessages(fork.Messages)
			runtime.SetContextTokens(0)
			runtime.SetUsage(agent.Usage{
				PromptTokens:     int(fork.PromptTokens),
				CompletionTokens: int(fork.CompletionTokens),
				TotalTokens:      int(fork.PromptTokens + fork.CompletionTokens),
			})
			fmt.Fprintf(out, "[branched from %s into %s]\n", fork.ParentSessionID, fork.Title)
		case "/plan":
			planHook.SetActive(!planHook.IsActive())
			if planHook.IsActive() {
				fmt.Fprintln(out, "[plan mode on] read-only analysis; the agent will submit a plan for approval")
			} else {
				fmt.Fprintln(out, "[plan mode off] full tool access restored")
			}
		case "/undo":
			if err := restoreLatestSnapshot(out, sessions, currentSession.ID); err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
			}
		case "/compact":
			_ = saveSession(sessions, currentSession, runtime)
			if err := runCompact(ctx, out, workingDir, runtime); err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
			} else {
				_ = saveSession(sessions, currentSession, runtime)
			}
		case "/newsession":
			_ = saveSession(sessions, currentSession, runtime)
			currentSession = sessions.Create("new session")
			runtime.SetMessages(nil)
			runtime.SetContextTokens(0)
			fmt.Fprintln(out, "[new session started]")
		case "/archive":
			if err := saveSession(sessions, currentSession, runtime); err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			path, err := sessions.Archive(currentSession.ID)
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			fmt.Fprintf(out, "[archived session: %s]\n%s\n", currentSession.Title, path)
			currentSession = sessions.Create("new session")
			runtime.SetMessages(nil)
			runtime.SetContextTokens(0)
			fmt.Fprintln(out, "[new session started]")
		case "/session", "/sessions":
			metas, err := sessions.List()
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			if len(metas) == 0 {
				fmt.Fprintln(out, "[no saved sessions]")
				continue
			}
			for i, meta := range metas {
				fmt.Fprintf(out, "  %d. %s  (%d msgs, %s)\n", i+1, meta.Title, meta.MessageN, meta.UpdatedAt.Format("2006-01-02 15:04"))
			}
			fmt.Fprint(out, "select session number (0 to cancel): ")
			if !scanner.Scan() {
				return scanner.Err()
			}
			choice := strings.TrimSpace(scanner.Text())
			idx, err := strconv.Atoi(choice)
			if err != nil || idx < 1 || idx > len(metas) {
				if choice != "0" && choice != "" {
					fmt.Fprintln(out, "[cancelled]")
				}
				continue
			}
			sess, err := sessions.Load(metas[idx-1].ID)
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			_ = saveSession(sessions, currentSession, runtime)
			currentSession = sess
			runtime.SetMessages(sess.Messages)
			runtime.SetContextTokens(0)
			runtime.SetUsage(agent.Usage{
				PromptTokens:     int(sess.PromptTokens),
				CompletionTokens: int(sess.CompletionTokens),
				TotalTokens:      int(sess.PromptTokens + sess.CompletionTokens),
			})
			fmt.Fprintf(out, "[switched to: %s]\n", sess.Title)
		case "/key":
			if err := configureDeepSeekKey(scanner, out, workingDir, &cfg, ""); err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			if subProvider, perr := newSubagentProvider(cfg, workingDir); perr == nil {
				extras.subagent = newSubagentRunner(subProvider, workingDir, cfg)
			}
			runtime, err = newRuntime(workingDir, cfg, scanner, out, extras)
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			fmt.Fprintln(out, "[deepseek key saved]")
		case "/name":
			fmt.Fprintf(out, "user: %s  assistant: %s\n", cfg.User, cfg.Assistant)
			fmt.Fprintln(out, "usage: /name user <name> | /name assistant <name> | /name <name>")
		case "/quit", "quit", "exit":
			return nil
		default:
			if strings.HasPrefix(input, "/") && !strings.HasPrefix(input, "/name ") &&
				!strings.HasPrefix(input, "/key ") {
				fmt.Fprintf(out, "unknown command: %s (try /help)\n", input)
				continue
			}
			if strings.HasPrefix(input, "/name ") {
				fields := strings.Fields(strings.TrimPrefix(input, "/name "))
				var user, assistant string
				switch fields[0] {
				case "user", "u":
					user = strings.Join(fields[1:], " ")
				case "assistant", "a":
					assistant = strings.Join(fields[1:], " ")
				default:
					user = strings.Join(fields, " ")
					assistant = user
				}
				if user != "" {
					cfg.User = user
				}
				if assistant != "" {
					cfg.Assistant = assistant
				}
				if err := config.Save(filepath.Join(workingDir, "config.json"), cfg); err != nil {
					fmt.Fprintf(out, "error: %v\n", err)
					continue
				}
				fmt.Fprintf(out, "[names updated] %s / %s\n", cfg.User, cfg.Assistant)
				continue
			}
			if strings.HasPrefix(input, "/key ") {
				key := strings.TrimSpace(strings.TrimPrefix(input, "/key "))
				if err := configureDeepSeekKey(scanner, out, workingDir, &cfg, key); err != nil {
					fmt.Fprintf(out, "error: %v\n", err)
					continue
				}
				if subProvider, perr := newSubagentProvider(cfg, workingDir); perr == nil {
					extras.subagent = newSubagentRunner(subProvider, workingDir, cfg)
				}
				runtime, err = newRuntime(workingDir, cfg, scanner, out, extras)
				if err != nil {
					fmt.Fprintf(out, "error: %v\n", err)
					continue
				}
				fmt.Fprintln(out, "[deepseek key saved]")
				continue
			}
			runInput := input
			if planHook.IsActive() {
				runInput = "You are in plan mode. Do not modify any files or execute commands. " +
					"Analyze the request with read-only tools, then call exit_plan_mode with a concrete plan.\n\n" + input
			}
			if err := runtime.Run(ctx, runInput, renderEvent(out)); err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
			}
			_ = saveSession(sessions, currentSession, runtime)
			maybeRetitle(ctx, out, sessions, currentSession, runtime, cfg, workingDir)
		}
	}
}

// statusInput is everything the line-mode /status report needs.
type statusInput struct {
	workingDir string
	cfg        config.Config
	runtime    *agent.Runtime
	session    *session.Session
	planMode   bool
	allowed    map[string]bool
}

// printStatus prints the line-mode status report: workspace, provider, session,
// context estimate, provider-reported tokens and active permissions.
func printStatus(out io.Writer, in statusInput) {
	fmt.Fprintln(out, "status:")
	fmt.Fprintf(out, "  workspace: %s\n", in.workingDir)
	if branch := gitBranch(in.workingDir); branch != "" {
		fmt.Fprintf(out, "  git branch: %s\n", branch)
	}
	mode := "code"
	if in.planMode {
		mode = "plan (read-only)"
	}
	fmt.Fprintf(out, "  mode: %s\n", mode)
	fmt.Fprintf(out, "  permissions: %s · %s\n", in.cfg.Permissions.Mode, in.cfg.Permissions.Mode.Label())
	fmt.Fprintf(out, "  provider: %s  model: %s\n", in.cfg.Provider.Name, in.cfg.Provider.Model)
	if in.session != nil {
		fmt.Fprintf(out, "  session: %s  (%s)\n", in.session.Title, in.session.ID)
	}
	if in.runtime == nil {
		return
	}
	tokens := in.runtime.ContextTokens()
	window := in.cfg.Provider.EffectiveContextWindow()
	fmt.Fprintf(out, "  context: ~%d tokens of %d (%.0f%%), %d messages\n",
		tokens, window, contextPercent(tokens, window), len(in.runtime.Messages()))
	// The system prompt is a large fixed floor, so it is shown separately:
	// otherwise clearing the session leaves the total almost unchanged and
	// looks like the reset failed.
	details := in.runtime.ContextDetails()
	fmt.Fprintf(out, "           %d system prompt + tools, %d conversation\n",
		details.SystemTokens, details.ConversationTokens)
	usage := in.runtime.Usage()
	if usage.IsZero() {
		fmt.Fprintln(out, "  tokens: provider reports no usage")
	} else {
		fmt.Fprintf(out, "  tokens: prompt %d, completion %d, total %d\n",
			usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
	if len(in.allowed) > 0 {
		names := make([]string, 0, len(in.allowed))
		for name := range in.allowed {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(out, "  session-approved tools: %s\n", strings.Join(names, ", "))
	}
	if todos := todosText(in.session); todos != "" {
		fmt.Fprintf(out, "  todos:\n%s\n", indent(todos, "    "))
	}
}

// gitBranch reads the current branch from .git/HEAD without shelling out.
func gitBranch(workingDir string) string {
	data, err := os.ReadFile(filepath.Join(workingDir, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	const prefix = "ref: refs/heads/"
	if strings.HasPrefix(head, prefix) {
		return strings.TrimPrefix(head, prefix)
	}
	if len(head) >= 7 {
		return head[:7]
	}
	return ""
}

func todosText(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	items := tools.ParseTodos(sess.Todos)
	if len(items) == 0 {
		return ""
	}
	return tools.RenderTodos(items)
}

func indent(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func contextPercent(used, window int) float64 {
	if window <= 0 {
		return 0
	}
	return float64(used) / float64(window) * 100
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "mini-opencode is a fresh Go agent terminal project.")
	fmt.Fprintln(out, "permissions: /permissions [ask|auto-review|full-access] — 请求批准 / 帮我批准 / 完全访问；完全访问需追加 confirm。")
	fmt.Fprintln(out, "commands: /key <deepseek-api-key> saves a local key and switches provider to DeepSeek.")
	fmt.Fprintln(out, "sessions: active conversations are stored in .mini-opencode/sessions.db; /archive exports the current session to .mini-opencode/sessions/<id>.json.")
	fmt.Fprintln(out, "files: /undo restores the newest file snapshot recorded in this session.")
}

// printWorkspace reports the working directory and any additional allowed
// roots the agent may read and write outside the working directory.
func printWorkspace(out io.Writer, workingDir string, allowedRoots []string) {
	fmt.Fprintf(out, "workspace: %s\n", workingDir)
	if len(allowedRoots) == 0 {
		fmt.Fprintln(out, "allowed roots: (none)")
		return
	}
	fmt.Fprintln(out, "allowed roots:")
	for _, root := range allowedRoots {
		fmt.Fprintf(out, "  - %s\n", root)
	}
}

// saveSession persists the current runtime messages to the active session.
// It auto-titles untitled sessions from the first user message.
func saveSession(store *session.Store, sess *session.Session, rt *agent.Runtime) error {
	if store == nil || sess == nil || rt == nil {
		return nil
	}
	msgs := rt.Messages()
	sess.Messages = msgs
	usage := rt.Usage()
	sess.PromptTokens = int64(usage.PromptTokens)
	sess.CompletionTokens = int64(usage.CompletionTokens)
	if sess.Title == "new session" {
		for _, msg := range msgs {
			if msg.Role == agent.RoleUser && !strings.Contains(msg.Content, "<conversation_summary>") &&
				!agent.IsTodoContinuationText(msg.Content) {
				sess.Title = session.TitleFromMessage(msg.Content)
				break
			}
		}
	}
	return store.Save(sess)
}

func runCompact(ctx context.Context, out io.Writer, workingDir string, runtime *agent.Runtime) error {
	if len(runtime.Messages()) == 0 {
		fmt.Fprintln(out, "[nothing to compact yet]")
		return nil
	}
	before := len(runtime.Messages())
	summaryPrompt, err := prompt.SummarySystemPrompt(workingDir)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "[compacting context...]")
	result, err := runtime.CompactDetailed(ctx, summaryPrompt)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "[context compacted: %d messages -> 1]\n", before)
	if strings.TrimSpace(result.UserSummary) != "" {
		fmt.Fprintln(out, result.UserSummary)
	} else {
		fmt.Fprintln(out, "[summary saved internally]")
	}
	return nil
}

func toPromptSkills(installed []skills.Skill) []prompt.Skill {
	out := make([]prompt.Skill, 0, len(installed))
	for _, s := range installed {
		out = append(out, prompt.Skill{Name: s.Name, Description: s.Description, Location: s.Location})
	}
	return out
}

func printSkills(out io.Writer, workingDir string) {
	installed, err := skills.LoadSkills(workingDir)
	if err != nil {
		fmt.Fprintf(out, "error loading skills: %v\n", err)
		return
	}
	if len(installed) == 0 {
		fmt.Fprintln(out, "no skills installed")
	} else {
		fmt.Fprintln(out, "installed skills:")
		for _, s := range installed {
			fmt.Fprintf(out, "  %s\t%s\n", s.Name, s.Description)
		}
	}
	fmt.Fprintf(out, "curated available: %s\n", strings.Join(skills.CuratedNames(), ", "))
}

// runtimeExtras carries the pieces of the application that runtime
// construction needs but that are not part of the config: MCP tools and the
// session file observer.
type runtimeExtras struct {
	// Reused across CLI runtime rebuilds without changing persisted config.
	permissions *agent.ModePermissionPolicy
	mcpTools    []agent.Tool
	observer    tools.FileObserver
	todos       tools.TodoStore
	// planHook enforces plan mode; planApprover reviews submitted plans.
	planHook     *agent.PlanModeHook
	planApprover tools.PlanApprover
	// allowedTools is the session-wide permission allowlist ("always" answers).
	allowedTools map[string]bool
	// subagent runs delegated read-only investigations for the task tool.
	subagent tools.TaskRunner
	// todoReader lets the run loop drive the session task list.
	todoReader agent.TodoReader
	// memories is the cross-session note store. Nil disables both the memory
	// tool and recall injection.
	memories *memory.Store
	// memoryQuery seeds the recall injected into the system prompt: recalled
	// notes are scored against it, so it should describe what is being worked
	// on. Empty falls back to the most recently updated notes.
	memoryQuery string
}

// memoryRecallLimit bounds how many remembered notes are injected into a system
// prompt. Recall competes with the task itself for context, so it stays small:
// a handful of the most relevant notes is enough to orient the agent.
const memoryRecallLimit = 3

// printMemories lists stored memory notes, or searches them when a query is
// given. Memory is plain Markdown on disk, so this is a window onto files the
// user can also edit directly.
func printMemories(out io.Writer, workingDir, query string) {
	store := memory.NewStore(workingDir)
	fmt.Fprintf(out, "memory dir: %s\n", store.Dir())

	notes, err := store.Search(query, 20)
	if err != nil {
		fmt.Fprintf(out, "memory unavailable: %v\n", err)
		return
	}
	if len(notes) == 0 {
		if strings.TrimSpace(query) == "" {
			fmt.Fprintln(out, "no memories stored yet")
			// The directory only exists once something is written, so point at
			// it explicitly: memory is plain Markdown the user can author too.
			fmt.Fprintln(out, "notes are plain Markdown; write one with the memory tool or create the file by hand.")
		} else {
			fmt.Fprintf(out, "no memories matched %q\n", query)
		}
		return
	}
	for _, note := range notes {
		fmt.Fprintf(out, "\n## %s  (%s)\n", note.Title, note.Name)
		if len(note.Tags) > 0 {
			fmt.Fprintf(out, "tags: %s\n", strings.Join(note.Tags, ", "))
		}
		fmt.Fprintf(out, "updated: %s\n", note.UpdatedAt.Local().Format("2006-01-02 15:04"))
		fmt.Fprintln(out, indent(note.Body, "  "))
	}
	fmt.Fprintf(out, "\n(%d note(s))\n", len(notes))
}

// runRetries resolves the configured run-level retry count.
func runRetries(cfg config.Config) int {
	retries, set := cfg.Agent.Retries()
	if !set {
		return agent.DefaultRunRetries
	}
	return retries
}

// subagentRunnerFor returns the runner for the task tool, or nil when unset.
func subagentRunnerFor(extras runtimeExtras) tools.TaskRunner { return extras.subagent }

func newRuntime(workingDir string, cfg config.Config, scanner *bufio.Scanner, out io.Writer, extras runtimeExtras) (*agent.Runtime, error) {
	permissions := extras.permissions
	if permissions == nil {
		var err error
		permissions, err = agent.NewModePermissionPolicy(workingDir, cfg.Workspace.AllowedRoots, cfg.Permissions.Mode)
		if err != nil {
			return nil, err
		}
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
	// Recall happens before the prompt is used: notes relevant to what is
	// being worked on are appended to the system prompt so the agent starts
	// with what earlier sessions learned. The section is omitted entirely when
	// nothing is remembered, so a fresh workspace pays nothing for this.
	if recall := tools.MemoryRecallSection(extras.memories, extras.memoryQuery, memoryRecallLimit); recall != "" {
		systemPrompt = systemPrompt + "\n\n" + recall
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
		agent.WithPermissionConfirmer(confirmTool(scanner, out, extras.allowedTools, workingDir)),
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
	if memTool := tools.NewMemoryTool(tools.MemoryOptions{Store: extras.memories}); memTool != nil {
		options = append(options, agent.WithTool(memTool))
	}
	for _, tool := range extras.mcpTools {
		options = append(options, agent.WithTool(tool))
	}

	return agent.NewRuntime(
		provider,
		options...,
	), nil
}

// todoReaderFor exposes the session todo store to the run loop, honouring the
// agent.todo_chain switch.
func todoReaderFor(cfg config.Config, store tools.TodoStore) agent.TodoReader {
	if store == nil || !cfg.Agent.TodoChainEnabled() {
		return nil
	}
	reader, ok := store.(agent.TodoReader)
	if !ok {
		return nil
	}
	return reader
}

// mcpServerConfigs converts the persisted MCP settings into launch configs.
func mcpServerConfigs(servers map[string]config.MCPServerConfig) map[string]mcp.ServerConfig {
	if len(servers) == 0 {
		return nil
	}
	out := make(map[string]mcp.ServerConfig, len(servers))
	for name, cfg := range servers {
		out[name] = mcp.ServerConfig{Enabled: cfg.Enabled, Command: cfg.Command, Args: cfg.Args}
	}
	return out
}

// reportMCPFailures prints a warning for every configured server that could
// not start, so a broken config is visible instead of silently missing tools.
func reportMCPFailures(out io.Writer, manager *mcp.Manager) {
	for _, status := range manager.Statuses() {
		if status.Enabled && status.Err != "" {
			fmt.Fprintf(out, "warning: mcp server %q failed: %s\n", status.Name, status.Err)
		}
	}
}

// printMCPStatus renders the /mcp report.
func printMCPStatus(out io.Writer, manager *mcp.Manager) {
	statuses := manager.Statuses()
	if len(statuses) == 0 {
		fmt.Fprintln(out, "no MCP servers configured (config.json -> mcpServers)")
		return
	}
	fmt.Fprintln(out, "MCP servers:")
	for _, status := range statuses {
		fmt.Fprintf(out, "  %s\n", status)
	}
}

// confirmTool prompts on the terminal for tool approval. Answers:
//
//	y / yes / 允许   allow this call once
//	a / always       allow this tool for the rest of the session
//	anything else    deny
//
// Mutating file tools additionally print a diff preview so the decision is
// made against what actually changes.
func confirmTool(scanner *bufio.Scanner, out io.Writer, sessionAllowed map[string]bool, workingDir string) agent.PermissionConfirmer {
	return func(ctx context.Context, call agent.ToolCall, result agent.ToolResult) bool {
		if sessionAllowed[call.Name] {
			return true
		}
		if preview := toolDiffPreview(call, workingDir); preview != "" {
			fmt.Fprintf(out, "%s\n", preview)
		}
		fmt.Fprintf(out, "allow tool %s? [y] once · [a] always this session · [n] deny: ", call.Name)
		if !scanner.Scan() {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
		case "y", "yes", "允许":
			return true
		case "a", "always", "总是":
			sessionAllowed[call.Name] = true
			fmt.Fprintf(out, "[%s allowed for this session]\n", call.Name)
			return true
		default:
			return false
		}
	}
}

func newProvider(cfg config.ProviderConfig, workingDir string) (agent.Provider, error) {
	switch strings.ToLower(cfg.Name) {
	case "", "echo":
		return agent.EchoProvider{}, nil
	case "deepseek", "openai-compatible", "openai_compatible":
		apiKey := cfg.ResolvedAPIKeyFrom(workingDir)
		retries, retriesSet := cfg.Retries()
		return agent.NewOpenAICompatibleProvider(agent.OpenAICompatibleConfig{
			BaseURL:       cfg.BaseURL,
			APIKey:        apiKey,
			Model:         cfg.Model,
			Timeout:       cfg.RequestTimeout(),
			MaxRetries:    retries,
			MaxRetriesSet: retriesSet,
		})
	default:
		return nil, fmt.Errorf("unknown provider: %s", cfg.Name)
	}
}

func ensureProviderKey(scanner *bufio.Scanner, out io.Writer, workingDir string, cfg *config.Config) error {
	if strings.ToLower(cfg.Provider.Name) != "deepseek" {
		return nil
	}
	if cfg.Provider.ResolvedAPIKeyFrom(workingDir) != "" {
		return nil
	}
	return configureDeepSeekKey(scanner, out, workingDir, cfg, "")
}

func configureDeepSeekKey(scanner *bufio.Scanner, out io.Writer, workingDir string, cfg *config.Config, key string) error {
	if key == "" {
		fmt.Fprint(out, "DeepSeek API key: ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		key = strings.TrimSpace(scanner.Text())
	}
	if key == "" {
		return fmt.Errorf("deepseek api key is required")
	}
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
		return err
	}
	return config.Save(filepath.Join(workingDir, "config.json"), *cfg)
}

// cliPlanApprover prompts on the terminal for a plan verdict and clears plan
// mode when the user approves.
func cliPlanApprover(scanner *bufio.Scanner, out io.Writer, hook *agent.PlanModeHook) tools.PlanApprover {
	return planApproverFunc(func(ctx context.Context, plan string) (bool, string) {
		fmt.Fprintf(out, "\n[plan submitted]\n%s\n", strings.TrimSpace(plan))
		fmt.Fprint(out, "approve plan and start implementing? [y/N]: ")
		if !scanner.Scan() {
			return false, ""
		}
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if answer == "y" || answer == "yes" || answer == "允许" {
			if hook != nil {
				hook.SetActive(false)
			}
			return true, ""
		}
		return false, ""
	})
}

// planApproverFunc adapts a function to the tools.PlanApprover interface.
type planApproverFunc func(ctx context.Context, plan string) (bool, string)

func (f planApproverFunc) ApprovePlan(ctx context.Context, plan string) (bool, string) {
	return f(ctx, plan)
}

// maybeRetitle replaces the placeholder session title with a provider-generated
// one once the conversation has a first exchange. Failures are silent: a
// missing title is cosmetic, and the caller already saved the session.
func maybeRetitle(ctx context.Context, out io.Writer, store *session.Store, sess *session.Session,
	runtime *agent.Runtime, cfg config.Config, workingDir string) {
	if store == nil || sess == nil || runtime == nil || sess.Title != "new session" {
		return
	}
	first := firstUserMessage(runtime.Messages())
	if first == "" {
		return
	}
	generate := makeTitleGenerator(cfg, workingDir)
	if generate == nil {
		return
	}
	title, err := generate(ctx, first)
	if err != nil || title == "" {
		return
	}
	sess.Title = title
	if err := store.Save(sess); err == nil {
		fmt.Fprintf(out, "[session title: %s]\n", title)
	}
}

func renderEvent(out io.Writer) func(agent.Event) {
	streamed := false
	return func(event agent.Event) {
		switch event.Type {
		case agent.EventTurnStarted:
			fmt.Fprintf(out, "[turn %d]\n", event.Turn)
		case agent.EventAssistantDelta:
			if event.Delta != "" {
				fmt.Fprint(out, event.Delta)
				streamed = true
			}
		case agent.EventAssistantResponse:
			if streamed {
				// Content already printed token-by-token; just end the line.
				fmt.Fprintln(out)
				streamed = false
				return
			}
			if event.Message != nil && event.Message.Content != "" {
				fmt.Fprintln(out, event.Message.Content)
			}
		case agent.EventToolCallStarted:
			if event.ToolCall != nil {
				fmt.Fprintf(out, "tool: %s\n", event.ToolCall.Name)
			}
		case agent.EventToolCallFinished:
			if event.ToolResult == nil {
				return
			}
			if event.ToolResult.Error != "" {
				fmt.Fprintf(out, "tool error: %s\n", event.ToolResult.Error)
				return
			}
			fmt.Fprintf(out, "tool result: %s\n", event.ToolResult.Content)
		case agent.EventToolCallFailed, agent.EventToolPermissionRequired, agent.EventToolPermissionDenied:
			if event.ToolResult != nil && event.ToolResult.Error != "" {
				fmt.Fprintf(out, "tool error: %s\n", event.ToolResult.Error)
			}
		case agent.EventHookDenied:
			if event.ToolResult != nil && event.ToolResult.Error != "" {
				fmt.Fprintf(out, "hook blocked: %s\n", event.ToolResult.Error)
			}
		case agent.EventHookStopped:
			if event.Error != nil {
				fmt.Fprintf(out, "hook stopped run: %v\n", event.Error)
			}
		case agent.EventTodoContinuation:
			fmt.Fprintf(out, "[continuing with the next task]\n%s\n", event.Todos)
		case agent.EventTodoBlocked:
			fmt.Fprintf(out, "[todo chain paused] %s\n", event.BlockedReason)
			if strings.TrimSpace(event.BlockedNeeds) != "" {
				fmt.Fprintf(out, "[needs] %s\n", event.BlockedNeeds)
			}
		case agent.EventTodoChainStopped:
			if event.Error != nil {
				fmt.Fprintf(out, "warning: todo chain stopped: %v\n", event.Error)
			}
		case agent.EventRunRetry:
			if event.Error != nil {
				fmt.Fprintf(out, "[retry %d] %v\n", event.Attempt, event.Error)
			}
		case agent.EventProviderWarning:
			if event.Error != nil {
				fmt.Fprintf(out, "warning: provider: %v\n", event.Error)
			}
		case agent.EventContextCompacted:
			if event.Error != nil {
				fmt.Fprintf(out, "warning: automatic compaction failed: %v\n", event.Error)
			} else {
				fmt.Fprintf(out, "[context auto-compacted at ~%d tokens]\n", event.ContextTokens)
				if strings.TrimSpace(event.Plan) != "" {
					fmt.Fprintln(out, strings.TrimSpace(event.Plan))
				}
			}
		case agent.EventPlanSubmitted:
			if strings.TrimSpace(event.Plan) != "" {
				fmt.Fprintln(out, "[plan]")
				fmt.Fprintln(out, strings.TrimSpace(event.Plan))
			}
		case agent.EventTodosChanged:
			if event.Todos != "" {
				fmt.Fprintf(out, "[todos]\n%s\n", event.Todos)
			}
		case agent.EventUsage:
			if event.Usage != nil {
				fmt.Fprintf(out, "[tokens] prompt %d, completion %d, total %d\n",
					event.Usage.PromptTokens, event.Usage.CompletionTokens, event.Usage.TotalTokens)
			}
		}
	}
}
