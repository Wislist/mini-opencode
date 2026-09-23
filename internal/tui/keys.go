package tui

import (
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
	case stateKeyPrompt:
		return m.handleKeyPromptKey(msg)
	case stateQuitting:
		return m, tea.Quit
	case stateCompacting:
		return m.handleCompactingKey(msg)
	case stateSessionList:
		return m.handleSessionListKey(msg)
	case statePlanApproval:
		return m.handlePlanApprovalKey(msg)
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
			m.syncInputHeight()
			m.commandFiltered = nil
			m.commandCursor = 0
			if selected != "" {
				return m.handleInput(selected)
			}
		}
		input := strings.TrimSpace(m.input.Value())
		if input == "" {
			return m, nil
		}
		m.input.Reset()
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
	case "ctrl+t":
		// The panel is most likely to be in the way while the agent is working,
		// so folding is available during a run too.
		m.toggleTodos()
	case "ctrl+j", "shift+enter", "alt+enter":
		// Drafting the next message while the agent works is a normal thing to
		// do, so the newline keys stay live during a run.
		m.insertNewline()
	}
	return m, nil
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

func (m *Model) handleKeyPromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		key := strings.TrimSpace(m.keyInput.Value())
		m.keyInput.Reset()
		if key == "" {
			m.state = stateIdle
			m.addBlock(errorStyle.Render("key input cancelled"))
			m.refreshViewport()
			return m, textinput.Blink
		}
		return m.saveKey(key)
	case "ctrl+c", "esc":
		m.keyInput.Reset()
		m.state = stateIdle
		return m, textinput.Blink
	}
	var cmd tea.Cmd
	m.keyInput, cmd = m.keyInput.Update(msg)
	return m, cmd
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
