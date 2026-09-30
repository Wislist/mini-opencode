package tui

import (
	tea "charm.land/bubbletea/v2"
	"strings"

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
	// Bare /permissions opens the picker as a bottom overlay. The alternative --
	// printing the choices into the transcript -- mixed a menu into the
	// conversation and left it in the scrollback after the choice was made.
	if len(strings.Fields(input)) == 1 {
		m.openPermissionsMenu()
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
