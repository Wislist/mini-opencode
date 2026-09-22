package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wislist/mini-opencode/internal/agent"
)

func (m *Model) handleInput(input string) (tea.Model, tea.Cmd) {
	switch {
	case input == "/quit" || input == "/exit" || input == "quit" || input == "exit":
		m.state = stateQuitting
		return m, tea.Quit
	case input == "/help":
		m.addBlock(m.renderHelp())
		m.refreshViewport()
		return m, nil
	case input == "/version":
		m.addBlock(fmt.Sprintf("mini-opencode %s", m.version))
		m.refreshViewport()
		return m, nil
	case input == "/tools":
		m.addBlock(m.renderTools())
		m.refreshViewport()
		return m, nil
	case input == "/workspace":
		m.addBlock(m.renderWorkspace())
		m.refreshViewport()
		return m, nil
	case input == "/mcp":
		m.addBlock(m.renderMCPStatus())
		m.refreshViewport()
		return m, nil
	case input == "/init":
		return m.startInit()
	case input == "/fork":
		return m.handleForkSession()
	case input == "/plan":
		m.toggleMode()
		m.addBlock(toolArrow.Render("mode: " + m.mode.String()))
		m.refreshViewport()
		return m, nil
	case input == "/undo":
		return m.handleUndo()
	case input == "/status":
		m.gitStatus = collectGitStatus(m.workingDir)
		m.addBlock(m.renderStatus())
		m.refreshViewport()
		return m, nil
	case input == "/key":
		m.state = stateKeyPrompt
		m.keyInput.Reset()
		m.keyInput.Focus()
		return m, textinput.Blink
	case strings.HasPrefix(input, "/key "):
		return m.saveKey(strings.TrimSpace(strings.TrimPrefix(input, "/key ")))
	case input == "/name":
		m.addBlock(m.renderNames())
		m.refreshViewport()
		return m, nil
	case strings.HasPrefix(input, "/name "):
		return m.handleName(input)
	case input == "/compact":
		return m.startCompact()
	case input == "/memory", strings.HasPrefix(input, "/memory "):
		return m.handleMemory(input)
	case input == "/archive":
		return m.handleArchiveSession()
	case input == "/newsession":
		return m.handleNewSession()
	case input == "/session", input == "/sessions":
		return m.handleListSessions()
	case strings.HasPrefix(input, "/"):
		m.addBlock(errorStyle.Render("unknown command: " + input))
		m.refreshViewport()
		return m, nil
	}

	if m.runtime == nil {
		m.addBlock(errorStyle.Render("no runtime available. use /key to configure."))
		m.refreshViewport()
		return m, nil
	}

	m.addBlock(m.renderUserMessage(input))
	return m.startRun(input)
}

// startRun launches a runtime run for input, streaming events into the TUI.
func (m *Model) startRun(input string) (tea.Model, tea.Cmd) {
	m.state = stateRunning
	m.input.Blur()

	sendText := input
	if m.mode == ModePlan {
		sendText = "You are in plan mode. Do not modify any files or execute commands. " +
			"Analyze the request, inspect the codebase with read-only tools, and provide " +
			"a detailed plan with suggestions only.\n\n" + input
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.cancel = cancel

	go func() {
		err := m.runtime.Run(ctx, sendText, func(event agent.Event) {
			m.program.Send(runtimeEventMsg{event: event})
		})
		m.program.Send(runtimeDoneMsg{err: err})
	}()

	return m, spinner.Tick
}

// startDistill extracts durable memories from the conversation that just
// finished. It runs in the background after the run is over: extraction is a
// second provider call, and making the user wait for it would be a visible
// regression for a feature whose whole point is to stay unobtrusive.
func (m *Model) startDistill() tea.Cmd {
	if m.memoryDistiller == nil || m.runtime == nil {
		return nil
	}
	messages := distillMessagesFor(m.runtime.Messages())
	if len(messages) == 0 {
		return nil
	}
	distiller := m.memoryDistiller
	return func() tea.Msg {
		// Detached from the run context so cancelling the run does not cancel
		// the extraction its transcript earned.
		ctx, cancel := context.WithTimeout(context.Background(), distillTimeout)
		defer cancel()
		result, err := distiller.Extract(ctx, messages)
		return memoryDistilledMsg{notes: len(result.Notes), err: err}
	}
}

func (m *Model) saveKey(key string) (tea.Model, tea.Cmd) {
	if m.keySaver != nil {
		newCfg, err := m.keySaver(key)
		if err != nil {
			m.addBlock(errorStyle.Render("✗ " + err.Error()))
		} else {
			*m.cfg = newCfg
			m.addBlock(toolArrow.Render("[deepseek key saved]"))
			if m.runtimeFactory != nil {
				rt, rerr := m.runtimeFactory(newCfg)
				if rerr != nil {
					m.addBlock(errorStyle.Render("✗ " + rerr.Error()))
				} else {
					m.SetRuntime(rt)
				}
			}
		}
	}
	m.state = stateIdle
	m.refreshViewport()
	return m, textinput.Blink
}

// handleName parses a "/name" command. Supported forms:
//
//	/name                    show current names
//	/name user <name>        set the user display name
//	/name assistant <name>   set the assistant display name
//	/name <name>             set both names to <name>
func (m *Model) handleName(input string) (tea.Model, tea.Cmd) {
	args := strings.TrimSpace(strings.TrimPrefix(input, "/name"))
	fields := strings.Fields(args)
	if len(fields) == 0 {
		m.addBlock(m.renderNames())
		m.refreshViewport()
		return m, nil
	}

	var user, assistant string
	switch fields[0] {
	case "user", "u":
		if len(fields) < 2 {
			m.addBlock(errorStyle.Render("usage: /name user <name>"))
			m.refreshViewport()
			return m, nil
		}
		user = strings.Join(fields[1:], " ")
	case "assistant", "a":
		if len(fields) < 2 {
			m.addBlock(errorStyle.Render("usage: /name assistant <name>"))
			m.refreshViewport()
			return m, nil
		}
		assistant = strings.Join(fields[1:], " ")
	default:
		// "/name <value>" sets both labels at once.
		user = strings.Join(fields, " ")
		assistant = user
	}

	if m.nameSaver == nil {
		m.addBlock(errorStyle.Render("name saver not configured"))
		m.refreshViewport()
		return m, nil
	}
	newCfg, err := m.nameSaver(user, assistant)
	if err != nil {
		m.addBlock(errorStyle.Render("✗ " + err.Error()))
	} else {
		*m.cfg = newCfg
		m.addBlock(toolArrow.Render("[names updated] " +
			m.userLabelStyle().Render(m.cfg.User) + " / " + assistantLabel.Render(m.cfg.Assistant)))
	}
	m.state = stateIdle
	m.refreshViewport()
	return m, textinput.Blink
}

// startInit runs one analysis turn with the initialize prompt template so the
// agent can create or refresh AGENTS.md, then restores the normal prompt.
func (m *Model) startInit() (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addBlock(errorStyle.Render("no runtime available. use /key to configure."))
		m.refreshViewport()
		return m, nil
	}
	if m.initPromptProvider == nil {
		m.addBlock(errorStyle.Render("init prompt is not configured"))
		m.refreshViewport()
		return m, nil
	}
	system, err := m.initPromptProvider()
	if err != nil {
		m.addBlock(errorStyle.Render("✗ init: " + err.Error()))
		m.refreshViewport()
		return m, nil
	}
	m.systemPromptRestore = m.runtime.SystemPrompt()
	m.runtime.SetSystemPrompt(system)
	m.addBlock(toolArrow.Render("analyzing the repository to write AGENTS.md"))
	return m.startRun(initUserPrompt)
}

// maybeGenerateTitle asks the provider for a title while the session still has
// its placeholder name, so /session lists stay readable.
func (m *Model) maybeGenerateTitle() tea.Cmd {
	if m.titleGenerator == nil || m.runtime == nil || m.currentSession == nil {
		return nil
	}
	if m.currentSession.Title != "new session" {
		return nil
	}
	first := firstUserMessage(m.runtime.Messages())
	if first == "" {
		return nil
	}
	generate := m.titleGenerator
	return func() tea.Msg {
		title, err := generate(context.Background(), first)
		return sessionTitleMsg{title: title, err: err}
	}
}

// handleUndo restores the newest file snapshot recorded in this session.
func (m *Model) handleUndo() (tea.Model, tea.Cmd) {
	if m.snapshotRestorer == nil {
		m.addBlock(errorStyle.Render("snapshot restore is not configured"))
		m.refreshViewport()
		return m, nil
	}
	path, err := m.snapshotRestorer()
	if err != nil {
		m.addBlock(errorStyle.Render("✗ undo: " + err.Error()))
	} else {
		m.addBlock(toolArrow.Render("restored " + path))
		m.gitStatus = collectGitStatus(m.workingDir)
	}
	m.refreshViewport()
	return m, nil
}

func (m *Model) startCompact() (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addBlock(errorStyle.Render("no runtime available. use /key to configure."))
		m.refreshViewport()
		return m, nil
	}
	if len(m.runtime.Messages()) == 0 {
		m.addBlock(dimStyle.Render("nothing to compact yet"))
		m.refreshViewport()
		return m, nil
	}
	if m.compactor == nil {
		m.addBlock(errorStyle.Render("compactor not configured"))
		m.refreshViewport()
		return m, nil
	}
	before := len(m.runtime.Messages())
	m.saveCurrentSession()
	m.state = stateCompacting
	m.input.Blur()
	m.addBlock(dimStyle.Render(fmt.Sprintf("compacting context (%d messages)...", before)))
	m.refreshViewport()

	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.cancel = cancel

	go func() {
		result, err := m.compactor(ctx)
		m.program.Send(compactDoneMsg{before: before, userSummary: result.UserSummary, err: err})
	}()

	return m, spinner.Tick
}

// handleRuntimeEvent applies one runtime event to the model. It returns a
// command when the event scheduled deferred work, such as the throttled repaint
// of a streaming response; the caller must run it or the timer never fires and
// the streamed text never appears.
func (m *Model) handleRuntimeEvent(event agent.Event) tea.Cmd {
	switch event.Type {
	case agent.EventRunStarted:
		m.saveCurrentSession()
	case agent.EventAssistantDelta:
		if event.Delta == "" {
			return nil
		}
		// Accumulating the delta is cheap; rendering it is not. Appending here
		// and rendering on a timer keeps a fast token stream from re-running
		// the Markdown renderer (and re-laying out the transcript) once per
		// token, which is what made long responses stutter and starve the
		// spinner.
		m.streamingText += event.Delta
		m.queueSessionFlush([]agent.Message{{Role: agent.RoleAssistant, Content: m.streamingText}})
		return m.scheduleStreamRender()
	case agent.EventAssistantResponse:
		wasStreaming := m.streamingIdx >= 0
		// Any throttled frame still queued is now stale: the authoritative
		// content below supersedes it, and letting the timer fire afterwards
		// would repaint the block with a partial render.
		m.cancelStreamRender()
		if event.Message != nil && event.Message.Content != "" {
			// If we were streaming, replace the in-progress block with the
			// authoritative final content. Otherwise (tool-only response with
			// no content deltas) add a fresh block.
			if wasStreaming {
				m.blocks[m.streamingIdx] = m.renderAssistantMessage(event.Message.Content)
				m.markBlocksChanged()
			} else {
				m.addBlock(m.renderAssistantMessage(event.Message.Content))
			}
		}
		m.streamingIdx = -1
		m.streamingText = ""
		m.saveCurrentSession()
	case agent.EventToolCallStarted:
		if event.ToolCall != nil {
			m.addBlock(m.renderToolCall(event.ToolCall))
		}
	case agent.EventToolCallFinished:
		if event.ToolResult != nil {
			resultWidth := max(1, m.width-2)
			if event.ToolResult.Error != "" {
				m.addBlock(toolError.Width(resultWidth).Render("✗ " + event.ToolResult.Error))
			} else {
				content := event.ToolResult.Content
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				m.addBlock(toolArrow.Width(resultWidth).Render("→ " + content))
			}
		}
		m.saveCurrentSession()
	case agent.EventToolCallFailed, agent.EventToolPermissionDenied:
		errWidth := max(1, m.width-2)
		if event.ToolResult != nil && event.ToolResult.Error != "" {
			m.addBlock(toolError.Width(errWidth).Render("✗ " + event.ToolResult.Error))
		}
		if event.Error != nil && event.Error.Error() != "" {
			m.addBlock(toolError.Width(errWidth).Render("✗ " + event.Error.Error()))
		}
	case agent.EventTodoContinuation:
		// The event carries the rendered list, so the panel and the notice stay
		// in step without re-reading the store.
		m.todos = event.Todos
		m.addBlock(toolArrow.Render("→ continuing with the next task"))
		m.addBlock(dimStyle.Render(indentLines(event.Todos, "  ")))
	case agent.EventTodoBlocked:
		reason := strings.TrimSpace(event.BlockedReason)
		m.addBlock(permAsk.Render("⏸ paused: " + reason))
		if needs := strings.TrimSpace(event.BlockedNeeds); needs != "" {
			m.addBlock(dimStyle.Render("needs: " + needs))
		}
	case agent.EventTodoChainStopped:
		if event.Error != nil {
			m.addBlock(errorStyle.Render("! todo chain stopped: " + event.Error.Error()))
		}
	case agent.EventRunRetry:
		if event.Error != nil {
			m.addBlock(dimStyle.Render(fmt.Sprintf("⟳ retrying the turn (attempt %d): %v",
				event.Attempt, event.Error)))
		}
	case agent.EventProviderWarning:
		if event.Error != nil {
			m.addBlock(toolError.Width(max(1, m.width-2)).Render("! provider: " + event.Error.Error()))
		}
	case agent.EventContextCompacted:
		if event.Error != nil {
			m.addBlock(errorStyle.Render("✗ automatic compaction failed: " + event.Error.Error()))
			return nil
		}
		m.addBlock(toolArrow.Render(fmt.Sprintf(
			"⟳ context auto-compacted at ~%s tokens; the summary replaced the transcript",
			formatTokens(event.ContextTokens))))
		if strings.TrimSpace(event.Plan) != "" {
			m.addBlock(m.renderAssistantMessage(event.Plan))
		}
		return nil
	case agent.EventUsage:
		// Token accounting updates the status line, which is rebuilt from the
		// runtime totals on every render, so nothing to append here.
		return nil
	}
	if event.Type == agent.EventToolCallFailed || event.Type == agent.EventToolPermissionDenied || event.Type == agent.EventHookDenied || event.Type == agent.EventHookStopped || event.Type == agent.EventRunFailed || event.Type == agent.EventRunFinished {
		m.saveCurrentSession()
	}
	m.refreshViewport()
	return nil
}
