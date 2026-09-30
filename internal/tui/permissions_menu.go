package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
)

// permissionMenuItems is the mode picker, in the order shown. It mirrors the
// three modes the runtime accepts; the labels match what /permissions prints in
// the line-mode CLI so both entry points describe the modes the same way.
var permissionMenuItems = []struct {
	mode  agent.PermissionMode
	label string
	help  string
}{
	{agent.PermissionModeFullAccess, "完全信任", "跳过逐次审批（需确认）"},
	{agent.PermissionModeAutoReview, "替我审核", "保守本地规则，不确定时询问"},
	{agent.PermissionModeAsk, "请求批准", "每次危险操作都询问"},
}

// openPermissionsMenu enters the picker with the cursor on the active mode, so
// the common case (looking at the choices, then leaving) starts where the user
// already is.
func (m *Model) openPermissionsMenu() {
	m.state = statePermissions
	m.permissionsCursor = m.permissionModeIndex(m.PermissionMode())
	m.invalidateFooter()
	m.refreshViewport()
}

// permissionModeIndex returns the menu position of mode, or 0 when unknown.
func (m *Model) permissionModeIndex(mode agent.PermissionMode) int {
	for i, item := range permissionMenuItems {
		if item.mode == mode {
			return i
		}
	}
	return 0
}

// renderPermissionsMenu draws the picker over the footer.
func (m *Model) renderPermissionsMenu() string {
	w := boxWidth(m)
	inner := max(1, w-4)

	var lines []string
	lines = append(lines,
		keyLabel.Render("permissions:")+"  "+
			dimStyle.Render("↑↓ select · enter apply · esc cancel"))

	for i, item := range permissionMenuItems {
		marker := "  "
		label := item.label
		help := item.help
		if i == m.permissionsCursor {
			marker = "▶ "
			label = toolName.Render(label)
			help = dimStyle.Render(help)
		} else {
			label = dimStyle.Render(label)
			help = dimStyle.Render(help)
		}
		// Mark the mode that is currently active, so the list reads as "where am
		// I" and not just "what can I pick".
		current := " "
		if item.mode == m.PermissionMode() {
			current = "*"
		}
		lines = append(lines, fmt.Sprintf("%s%s %-10s %s", marker, current, label, help))
	}
	lines = append(lines, dimStyle.Render("仅本次进程生效；不是操作系统沙箱。"))

	// Wrap and then flatten into physical rows before trimming.
	//
	// wordWrap returns a multi-line string per entry, so trimming the slice
	// without splitting first counts logical entries, not the rows they occupy —
	// a narrow terminal folds the help text into extra rows and the menu grew
	// past the window while appearing to be trimmed.
	var rows []string
	for _, line := range lines {
		rows = append(rows, strings.Split(wordWrap(line, inner), "\n")...)
	}
	rows = m.fitPermissionMenuRows(rows)
	return sessionBox.Width(w).Render(strings.Join(rows, "\n"))
}

// fitPermissionMenuRows trims optional rows so the menu fits the terminal.
//
// The header row and the three mode rows are always kept; the trailing note
// goes first, then the per-item help text. Losing the note is a far smaller
// cost than pushing the input box off the screen or cutting the box open.
func (m *Model) fitPermissionMenuRows(lines []string) []string {
	return m.fitMenuRows(lines, min(len(lines), 4))
}

// renderPermissionsConfirm draws the full-trust confirmation step.
func (m *Model) renderPermissionsConfirm() string {
	w := boxWidth(m)
	inner := max(1, w-4)
	content := keyLabel.Render("完全信任确认") + "\n" +
		dimStyle.Render("将跳过逐次审批并解除工具层工作区限制；可访问当前用户有权限的文件和网络。") + "\n" +
		dimStyle.Render("安全 Hook、plan 模式和先读后写仍然有效。") + "\n" +
		permAsk.Render("enter 确认 · esc 取消")
	return permBox.Width(w).Render(wordWrap(content, inner))
}

// handlePermissionsMenuKey drives the picker.
func (m *Model) handlePermissionsMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "shift+tab":
		if m.permissionsCursor > 0 {
			m.permissionsCursor--
		}
		m.invalidateFooter()
		m.refreshViewport()
	case "down", "j", "tab":
		if m.permissionsCursor < len(permissionMenuItems)-1 {
			m.permissionsCursor++
		}
		m.invalidateFooter()
		m.refreshViewport()
	case "enter":
		item := permissionMenuItems[m.permissionsCursor]
		if item.mode == agent.PermissionModeFullAccess && m.PermissionMode() != item.mode {
			// Full access keeps its explicit second step: it removes the
			// per-call approval gate, so a single keypress must not be enough.
			m.state = statePermissionsConfirm
			m.invalidateFooter()
			m.refreshViewport()
			return m, nil
		}
		return m.applyPermissionMode(item.mode)
	case "esc", "ctrl+c", "q":
		m.closePermissionsMenu()
	}
	return m, nil
}

// handlePermissionsConfirmKey drives the full-access confirmation.
func (m *Model) handlePermissionsConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		return m.applyPermissionMode(agent.PermissionModeFullAccess)
	case "esc", "ctrl+c", "n":
		// Back to the picker rather than straight out: the user was mid-choice.
		m.state = statePermissions
		m.invalidateFooter()
		m.refreshViewport()
	}
	return m, nil
}

// applyPermissionMode switches the runtime mode and leaves the overlay.
func (m *Model) applyPermissionMode(mode agent.PermissionMode) (tea.Model, tea.Cmd) {
	if m.runtime != nil {
		if err := m.runtime.SetPermissionMode(mode); err != nil {
			m.closePermissionsMenu()
			m.addBlock(errorStyle.Render(err.Error()))
			m.refreshViewport()
			return m, nil
		}
	}
	m.permissionMode = mode
	// Grants made under the previous mode do not carry over: the user is
	// re-deciding how much trust to extend.
	clear(m.sessionAllowed)
	m.closePermissionsMenu()
	return m, nil
}

// closePermissionsMenu returns to the transcript without writing anything.
func (m *Model) closePermissionsMenu() {
	m.state = stateIdle
	m.invalidateFooter()
	m.refreshViewport()
}
