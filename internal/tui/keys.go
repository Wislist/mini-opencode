package tui

import (
	"runtime"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateIdle:
		return m.handleIdleKey(msg)
	case stateRunning:
		return m.handleRunningKey(msg)
	case statePermission:
		return m.handlePermissionKey(msg)
	case stateQuitting:
		return m, tea.Quit
	case stateCompacting:
		return m.handleCompactingKey(msg)
	case stateSessionList:
		return m.handleSessionListKey(msg)
	case statePlanApproval:
		return m.handlePlanApprovalKey(msg)
	case statePermissions:
		return m.handlePermissionsMenuKey(msg)
	case statePermissionsConfirm:
		return m.handlePermissionsConfirmKey(msg)
	case stateProviderMenu:
		return m.handleProviderMenuKey(msg)
	case stateModelMenu:
		return m.handleModelMenuKey(msg)
	case stateProviderSetup:
		return m.handleProviderSetupKey(msg)
	case stateProviderForm:
		return m.handleProviderFormKey(msg)
	case stateModelSelect:
		return m.handleModelSelectKey(msg)
	case stateModelsFetching:
		// esc abandons the wait; the late answer is dropped by its token.
		if msg.String() == "esc" || msg.String() == "ctrl+c" {
			m.modelsFetchToken++
			m.pendingAdd = nil
			if m.modelsFetchCancel != nil {
				m.modelsFetchCancel()
				m.modelsFetchCancel = nil
			}
			m.state = stateIdle
			m.refreshViewport()
			return m, m.input.Focus()
		}
		return m, nil
	}
	return m, nil
}

// handlePlanApprovalKey resolves a submitted plan. Approving ends plan mode so
// the agent can continue into implementation; rejecting keeps plan mode on.
func (m *Model) handlePlanApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		if m.pendingPlan != nil {
			m.pendingPlan.resp <- planDecision{approved: true}
		}
		m.pendingPlan = nil
		m.mode = ModeCode
		if m.planHook != nil {
			m.planHook.SetActive(false)
		}
		m.state = stateRunning
		m.refreshViewport()
	case "n", "N", "esc":
		if m.pendingPlan != nil {
			m.pendingPlan.resp <- planDecision{approved: false}
		}
		m.pendingPlan = nil
		m.state = stateRunning
		m.refreshViewport()
	}
	return m, nil
}

func (m *Model) handleIdleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleIdleKeyForOS(msg, runtime.GOOS)
}

func (m *Model) handleIdleKeyForOS(msg tea.KeyMsg, goos string) (tea.Model, tea.Cmd) {
	if isInputSelectAllKey(msg.String(), goos) {
		m.inputSelectAll = m.input.Value() != ""
		return m, nil
	}

	if m.inputSelectAll {
		switch msg.String() {
		case "esc":
			m.cancelInputSelectAll()
			return m, nil
		case "backspace", "delete", "ctrl+h":
			m.clearSelectedInput()
			m.syncInputHeight()
			m.updateCommandMenu()
			return m, nil
		case "ctrl+j", "shift+enter", "alt+enter":
			m.clearSelectedInput()
			m.insertNewline()
			m.updateCommandMenu()
			return m, nil
		default:
			if msg.Key().Text != "" {
				m.clearSelectedInput()
			} else {
				// Navigation and application shortcuts collapse the synthetic
				// selection before keeping their existing behavior.
				m.cancelInputSelectAll()
			}
		}
	}

	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		m.state = stateQuitting
		return m, tea.Quit
	case "ctrl+j", "shift+enter", "alt+enter":
		// Shift+Enter is the expected newline key and works on terminals that
		// support the kitty keyboard protocol (Bubble Tea v2 negotiates it).
		// Ctrl+J (LF) and Alt+Enter (ESC CR) are distinct bytes that work even
		// where the protocol is unavailable, so they stay as fallbacks.
		m.insertNewline()
		return m, nil
	case "enter":
		if m.commandMenuOpen() && len(m.commandFiltered) > 0 {
			selected := m.commandMenuSelect()
			m.input.Reset()
			m.cancelInputSelectAll()
			m.syncInputHeight()
			m.commandFiltered = nil
			m.commandCursor = 0
			if selected != "" {
				return m.handleInput(selected)
			}
		}
		input := strings.TrimSpace(m.input.Value())
		if input == "" {
			if m.llmStatus != LLMReady {
				m.openProviderRecoveryForm()
			}
			return m, nil
		}
		m.input.Reset()
		m.cancelInputSelectAll()
		m.syncInputHeight()
		m.commandFiltered = nil
		m.commandCursor = 0
		return m.handleInput(input)
	case "up":
		if m.commandMenuOpen() {
			m.commandMenuMove(-1)
			return m, nil
		}
		m.scrollLines(-keyScrollLines)
		return m, nil
	case "down":
		if m.commandMenuOpen() {
			m.commandMenuMove(1)
			return m, nil
		}
		m.scrollLines(keyScrollLines)
		return m, nil
	case "tab":
		if m.commandMenuOpen() && len(m.commandFiltered) > 0 {
			selected := m.commandMenuSelect()
			if selected != "" {
				m.input.SetValue(selected)
			}
			m.updateCommandMenu()
			return m, nil
		}
		m.toggleMode()
		return m, nil
	case "esc":
		// A selection is dismissed first: esc is the natural "never mind"
		// gesture, and keeping it would leave a highlighted region behind.
		if m.selection.active {
			m.clearSelection()
			return m, nil
		}
		if m.commandMenuOpen() {
			m.input.Reset()
			m.cancelInputSelectAll()
			m.commandFiltered = nil
			m.commandCursor = 0
			return m, nil
		}
	case "pgup":
		m.viewport.HalfPageUp()
		return m, nil
	case "pgdown":
		m.viewport.HalfPageDown()
		return m, nil
	case "ctrl+t":
		// ctrl+t folds the pinned task panel. Handled here rather than in the
		// input widget so the key is not swallowed as a text edit.
		m.toggleTodos()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Ordinary typing can soft-wrap just like an explicit newline. Recompute
	// the prompt height after the widget accepts the key so wrapped text does
	// not stay hidden inside a one-row textarea.
	m.syncInputHeight()
	m.updateCommandMenu()
	return m, cmd
}

func (m *Model) handleRunningKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.clearSelection()
		if m.cancel != nil {
			m.cancel()
		}
		m.state = stateIdle
		m.streamingIdx = -1
		m.streamingText = ""
		m.addBlock(errorStyle.Render("✗ interrupted"))
		m.refreshViewport()
		return m, textinput.Blink
	case "up":
		m.scrollLines(-keyScrollLines)
	case "down":
		m.scrollLines(keyScrollLines)
	case "pgup":
		m.viewport.HalfPageUp()
	case "pgdown":
		m.viewport.HalfPageDown()
	case "ctrl+t":
		// The panel is most likely to be in the way while the agent is working,
		// so folding is available during a run too.
		m.toggleTodos()
	case "enter", "ctrl+m":
		// The running turn still owns the runtime, so Enter must not start
		// another one. The draft stays in the composer and is sent once the
		// run finishes.
		return m, nil
	case "tab", "shift+tab", "ctrl+d":
		// Application-level keys keep their idle meaning (mode / quit) rather
		// than being inserted as text by the textarea.
		return m, nil
	case "ctrl+j", "shift+enter", "alt+enter":
		// Drafting the next message while the agent works is a normal thing to
		// do, so the newline keys stay live during a run.
		m.insertNewline()
		return m, nil
	}
	// Everything else is ordinary editing. Forwarding it keeps the composer
	// usable while the answer streams; dropping it here was why typing (and
	// especially Chinese IME commits) appeared to be swallowed during a run.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncInputHeight()
	return m, cmd
}

func (m *Model) handleCompactingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		if m.cancel != nil {
			m.cancel()
		}
		m.state = stateIdle
		m.input.Focus()
		m.addBlock(errorStyle.Render("✗ compact interrupted"))
		m.refreshViewport()
		return m, textinput.Blink
	case "up":
		m.scrollLines(-keyScrollLines)
	case "down":
		m.scrollLines(keyScrollLines)
	case "ctrl+t":
		// The panel is most likely to be in the way while the agent is working,
		// so folding is available during a run too.
		m.toggleTodos()
	}
	return m, nil
}

func (m *Model) handlePermissionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.resolvePermission(permissionDecision{allow: true})
	case "a", "A":
		// Approve and stop prompting for this tool for the rest of the session.
		m.resolvePermission(permissionDecision{allow: true, always: true})
	case "n", "N", "esc":
		m.resolvePermission(permissionDecision{allow: false})
	}
	return m, nil
}

// resolvePermission answers the pending permission request and records a
// session-wide approval when the user chose "always".
func (m *Model) resolvePermission(decision permissionDecision) {
	if m.pendingPerm != nil {
		if decision.always && decision.allow {
			m.allowToolForSession(m.pendingPerm.call.Name)
		}
		m.pendingPerm.resp <- decision
	}
	m.pendingPerm = nil
	m.state = stateRunning
	m.refreshViewport()
}

// toggleTodos folds or unfolds the pinned task panel. The fold state is
// visible from the panel itself — a one-line summary versus the full list —
// so the toggle deliberately writes nothing into the transcript: the panel is
// pinned chrome, not content.
func (m *Model) toggleTodos() {
	m.todosCollapsed = !m.todosCollapsed
	m.invalidateFooter()
	m.refreshViewport()
}
