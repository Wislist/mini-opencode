package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyCtrlD:
		m.state = stateQuitting
		return m, tea.Quit
	case tea.KeyEnter:
		if m.commandMenuOpen() && len(m.commandFiltered) > 0 {
			selected := m.commandMenuSelect()
			m.input.Reset()
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
		m.commandFiltered = nil
		m.commandCursor = 0
		return m.handleInput(input)
	case tea.KeyUp:
		if m.commandMenuOpen() {
			m.commandMenuMove(-1)
			return m, nil
		}
		m.scrollLines(-keyScrollLines)
		return m, nil
	case tea.KeyDown:
		if m.commandMenuOpen() {
			m.commandMenuMove(1)
			return m, nil
		}
		m.scrollLines(keyScrollLines)
		return m, nil
	case tea.KeyTab:
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
	case tea.KeyEsc:
		if m.commandMenuOpen() {
			m.input.Reset()
			m.commandFiltered = nil
			m.commandCursor = 0
			return m, nil
		}
	case tea.KeyPgUp:
		m.viewport.HalfViewUp()
		return m, nil
	case tea.KeyPgDown:
		m.viewport.HalfViewDown()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.updateCommandMenu()
	return m, cmd
}

func (m *Model) handleRunningKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		if m.cancel != nil {
			m.cancel()
		}
		m.state = stateIdle
		m.streamingIdx = -1
		m.streamingText = ""
		m.addBlock(errorStyle.Render("✗ interrupted"))
		m.refreshViewport()
		return m, textinput.Blink
	case tea.KeyUp:
		m.scrollLines(-keyScrollLines)
	case tea.KeyDown:
		m.scrollLines(keyScrollLines)
	}
	return m, nil
}

func (m *Model) handleCompactingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		if m.cancel != nil {
			m.cancel()
		}
		m.state = stateIdle
		m.input.Focus()
		m.addBlock(errorStyle.Render("✗ compact interrupted"))
		m.refreshViewport()
		return m, textinput.Blink
	case tea.KeyUp:
		m.scrollLines(-keyScrollLines)
	case tea.KeyDown:
		m.scrollLines(keyScrollLines)
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
	switch msg.Type {
	case tea.KeyEnter:
		key := strings.TrimSpace(m.keyInput.Value())
		m.keyInput.Reset()
		if key == "" {
			m.state = stateIdle
			m.addBlock(errorStyle.Render("key input cancelled"))
			m.refreshViewport()
			return m, textinput.Blink
		}
		return m.saveKey(key)
	case tea.KeyCtrlC, tea.KeyEsc:
		m.keyInput.Reset()
		m.state = stateIdle
		return m, textinput.Blink
	}
	var cmd tea.Cmd
	m.keyInput, cmd = m.keyInput.Update(msg)
	return m, cmd
}
