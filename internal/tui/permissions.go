package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
)

func (m *Model) PermissionMode() agent.PermissionMode {
	if m.permissionMode == "" {
		return agent.PermissionModeAsk
	}
	return m.permissionMode
}

func (m *Model) handlePermissions(input string) (tea.Model, tea.Cmd) {
	if m.state != stateIdle {
		m.addBlock(errorStyle.Render("请等待当前操作结束，再切换权限模式。"))
		m.refreshViewport()
		return m, nil
	}
	mode, changed, message := agent.PermissionCommand(m.PermissionMode(), input)
	if changed {
		if m.runtime != nil {
			if err := m.runtime.SetPermissionMode(mode); err != nil {
				m.addBlock(errorStyle.Render(err.Error()))
				m.refreshViewport()
				return m, nil
			}
		}
		m.permissionMode = mode
		clear(m.sessionAllowed)
		m.invalidateFooter()
	}
	m.addBlock(message)
	m.refreshViewport()
	return m, nil
}
