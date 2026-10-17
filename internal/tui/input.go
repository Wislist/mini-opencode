package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
)

const runtimeUnavailableMessage = "no runtime available. use /provider add to configure one, or /provider to switch."

func (m *Model) handleInput(input string) (tea.Model, tea.Cmd) {
	// The previous run may still be unwinding after an interrupt. A second Run
	// on the same runtime would interleave messages, so the input is queued and
	// sent from runtimeDoneMsg once the runtime is free.
	if m.runUnwinding && input != "/quit" && input != "/exit" && input != "quit" && input != "exit" {
		m.pendingInput = input
		return m, nil
	}
	switch {
	case input == "/quit" || input == "/exit" || input == "quit" || input == "exit":
		m.state = stateQuitting
		return m, tea.Quit
	case input == "/mcp":
		m.addBlock(m.renderMCPStatus())
		m.refreshViewport()
		return m, nil
	case input == "/init":
		return m.startInit()
	case input == "/fork":
		return m.handleForkSession()
	case input == "/undo":
		return m.handleUndo()
	case input == "/status":
		m.gitStatus = collectGitStatus(m.workingDir)
		m.addBlock(m.renderStatus())
		m.refreshViewport()
		return m, nil
	case input == "/permissions" || strings.HasPrefix(input, "/permissions "):
		return m.handlePermissions(input)
	case input == "/provider" || strings.HasPrefix(input, "/provider "):
		return m.handleProvider(input)
	case input == "/model" || strings.HasPrefix(input, "/model "):
		return m.handleModel(input)
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
	if m.llmStatus != LLMReady {
		m.input.SetValue(input)
		m.input.CursorEnd()
		m.syncInputHeight()
		m.openProviderRecoveryForm()
		return m, nil
	}

	if m.runtime == nil {
		m.addBlock(errorStyle.Render(runtimeUnavailableMessage))
		m.refreshViewport()
		return m, nil
	}

	if m.paused {
		return m.handleContinuation(input)
	}

	m.lastUserGoal = input
	m.addBlock(m.renderUserMessage(input))
	return m.startRun(input)
}

// handleContinuation sends the message that follows an interrupted run.
//
// Continuing is not a fresh turn: the partial answer and the task list are
// already in the runtime, and the goal may have been edited in place. The
// transcript shows the user's own words; the note prepended to the request tells
// the model that the previous run was interrupted and whether the goal changed.
func (m *Model) handleContinuation(input string) (tea.Model, tea.Cmd) {
	goal := strings.TrimSpace(m.lastUserGoal)
	unchanged := goal != "" && strings.TrimSpace(input) == goal
	sendText := m.continuationSendText(input)
	if unchanged {
		m.addBlock(toolArrow.Render("→ continue after interruption"))
	} else {
		m.addBlock(m.renderUserMessage(input))
	}
	m.lastUserGoal = input
	m.paused = false
	return m.startRunWith(sendText, true)
}

// continuationSendText describes a continuation to the model. It must be called
// before lastUserGoal is updated to the new input.
func (m *Model) continuationSendText(input string) string {
	goal := strings.TrimSpace(m.lastUserGoal)
	var send string
	if goal != "" && strings.TrimSpace(input) == goal {
		send = "[interrupted] The user paused the previous run and asked to continue with the same goal. " +
			"Continue from where it stopped. Keep the existing task list and update it with todo_write as you make progress."
	} else {
		send = "[goal adjusted after interruption] " + input + "\n\n" +
			"This replaces the goal of the interrupted run. Continue from where the previous run stopped, " +
			"update the task list to match the adjusted goal, and do not repeat completed work."
	}
	if m.mode == ModePlan {
		send = "You are in plan mode. Do not modify any files or execute commands. " +
			"Analyze the request, inspect the codebase with read-only tools, and provide " +
			"a detailed plan with suggestions only.\n\n" + send
	}
	return send
}

// startRun launches a runtime run for input, streaming events into the TUI.
func (m *Model) startRun(input string) (tea.Model, tea.Cmd) {
	sendText := input
	if m.mode == ModePlan {
		sendText = "You are in plan mode. Do not modify any files or execute commands. " +
			"Analyze the request, inspect the codebase with read-only tools, and provide " +
			"a detailed plan with suggestions only.\n\n" + input
	}
	return m.startRunWith(sendText, false)
}

// startRunWith launches a run with the exact text to send. keepTodos is set for
// continuations, which must resume the interrupted task list instead of
// clearing it at the turn boundary.
func (m *Model) startRunWith(sendText string, keepTodos bool) (tea.Model, tea.Cmd) {
	m.state = stateRunning
	m.paused = false
	m.pendingInput = ""
	// Keep the composer focused while the answer streams. Users draft their
	// next message during a run, and a blurred textarea drops every key before
	// the running-state handler can forward it. Focus also restarts the virtual
	// cursor blink command.
	focusCmd := m.input.Focus()
	if keepTodos {
		m.startNewTurnKeepingTodos(sendText)
	} else {
		m.startNewTurn(sendText)
	}
	// The new question may have been appended while the viewport still held the
	// previous frame, and the user may have scrolled up to read history. Sending
	// is the moment to pin the view to the newest content so the incoming answer
	// follows instead of streaming off screen.
	m.scrollToLatest()

	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.cancel = cancel

	go func() {
		err := m.runtime.Run(ctx, sendText, func(event agent.Event) {
			m.program.Send(runtimeEventMsg{event: event})
		})
		m.program.Send(runtimeDoneMsg{err: err})
	}()

	return m, tea.Batch(m.spinner.Tick, focusCmd)
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

// startInit runs one analysis turn with the initialize prompt template so the
// agent can create or refresh AGENTS.md, then restores the normal prompt.
func (m *Model) startInit() (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addBlock(errorStyle.Render(runtimeUnavailableMessage))
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
		m.addBlock(errorStyle.Render(runtimeUnavailableMessage))
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

	return m, m.spinner.Tick
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
		if m.runUnwinding {
			// The user already paused this run and its visible partial answer
			// was rendered then. Deltas still in flight must not resurrect the
			// stream for a run that is being torn down.
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
		if m.runUnwinding {
			// The turn completed just as the user interrupted. The runtime has
			// already recorded the full answer, so it is authoritative: replace
			// the partial render with it instead of showing both, and make sure
			// runtimeDoneMsg does not append the partial a second time.
			m.interruptedResponse = true
			m.interruptedPartial = ""
			m.cancelStreamRender()
			if event.Message != nil && event.Message.Content != "" {
				rendered := m.renderAssistantMessage(event.Message.Content)
				if m.hasInterruptedBlock && m.interruptedBlockIdx >= 0 && m.interruptedBlockIdx < len(m.blocks) {
					m.blocks[m.interruptedBlockIdx] = rendered
					m.markBlocksChanged()
				} else {
					m.addBlock(rendered)
				}
			}
			m.hasInterruptedBlock = false
			m.interruptedBlockIdx = 0
			m.streamingIdx = -1
			m.streamingText = ""
			m.streamCache = nil
			m.saveCurrentSession()
			m.refreshViewport()
			return nil
		}
		wasStreaming := m.streamingIdx >= 0
		// Any throttled frame still queued is now stale: the authoritative
		// content below supersedes it, and letting the timer fire afterwards
		// would repaint the block with a partial render.
		m.cancelStreamRender()
		if event.Message != nil && event.Message.Content != "" {
			// If we were streaming, replace the in-progress block with the
			// authoritative final content. Otherwise (tool-only response with
			// no content deltas) add a fresh block.
			//
			// The cached renderer is used here too: the final content extends
			// what the last throttled frame rendered, so its settled prefix is
			// still valid and only the tail needs a fresh Glamour pass.
			if wasStreaming {
				m.blocks[m.streamingIdx] = m.renderStreamingMessage(event.Message.Content)
				m.markBlocksChanged()
			} else {
				m.addBlock(m.renderAssistantMessage(event.Message.Content))
			}
		}
		m.streamingIdx = -1
		m.streamingText = ""
		// The stream is over: drop the prefix cache so the next message starts
		// clean and cannot reuse a block from this one.
		m.streamCache = nil
		m.saveCurrentSession()
	case agent.EventToolCallStarted:
		if event.ToolCall != nil {
			m.addBlock(m.renderToolCall(event.ToolCall))
		}
	case agent.EventToolCallFinished:
		if event.ToolResult != nil {
			m.addBlock(m.renderToolResult(event.ToolResult))
		}
		m.saveCurrentSession()
	case agent.EventToolCallFailed, agent.EventToolPermissionDenied:
		if event.ToolResult != nil && (event.ToolResult.Error != "" || event.ToolResult.Content != "") {
			m.addBlock(m.renderToolResult(event.ToolResult))
		}
		if event.Error != nil && event.Error.Error() != "" {
			errWidth := max(1, m.width-2)
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
