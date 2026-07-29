package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/wislist/mini-opencode/internal/agent"
)

// ── View ──────────────────────────────────────────────

func (m *Model) View() string {
	if m.width == 0 {
		return "loading..."
	}

	header := m.renderHeader()
	footer := m.renderFooter()
	m.fitViewport(header, footer)
	m.updateNativeCursorPosition(header, footer)

	sections := []string{header, m.viewport.View()}
	sections = append(sections, footer...)
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderFooter returns every section below the transcript viewport. Keeping
// these sections together lets fitViewport reserve their actual rendered
// height instead of relying on a fixed subtraction that leaves stale rows.
func (m *Model) renderFooter() []string {
	var sections []string
	if m.state == statePermission && m.pendingPerm != nil {
		sections = append(sections, m.renderPermissionPrompt())
	} else if m.state == stateKeyPrompt {
		sections = append(sections, m.renderKeyPrompt())
	} else if m.state == stateSessionList {
		sections = append(sections, m.renderSessionList())
	} else {
		if m.commandMenuOpen() && len(m.commandFiltered) > 0 {
			sections = append(sections, m.renderCommandMenu())
		}
		sections = append(sections, m.renderInputBar())
	}
	return append(sections, m.renderHelpBar())
}

// fitViewport makes the complete view occupy the terminal height. The old
// fixed "height - 7" viewport left two rows unmanaged in the common layout;
// after a resize those rows could retain or wrap a previous help bar, making
// the command hints appear twice.
func (m *Model) fitViewport(header string, footer []string) {
	if m.height <= 0 {
		return
	}
	height := m.height - lipgloss.Height(header)
	for _, section := range footer {
		height -= lipgloss.Height(section)
	}
	height = max(1, height)
	if m.viewport.Height == height {
		return
	}

	wasAtBottom := m.viewport.AtBottom()
	m.viewport.Height = height
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) renderHeader() string {
	left := headerStyle.Render("◆ mini-opencode")
	titleW := lipgloss.Width(left)
	// Progressively drop header segments as the terminal narrows so the
	// header never overflows or overlaps itself.
	gitSeg := m.renderGitSegment()
	sessionSeg := m.renderSessionSegment()
	modeSeg := m.renderModeSegment()
	versionSeg := dimStyle.Render(fmt.Sprintf("v%s · %s", m.version, m.cfg.Provider.Name))
	ctxSeg := m.renderContextSegment()

	// Build the right side, dropping context first then version.
	var right string
	rightW := 0
	if m.width >= titleW+lipgloss.Width(versionSeg)+lipgloss.Width(ctxSeg)+4 {
		right = ctxSeg + "  " + versionSeg
		rightW = lipgloss.Width(right)
	} else if m.width >= titleW+lipgloss.Width(versionSeg)+4 {
		right = versionSeg
		rightW = lipgloss.Width(right)
	}

	// Budget for left-side segments after reserving the right side.
	budget := m.width - titleW - rightW - 2
	if budget >= lipgloss.Width(gitSeg)+lipgloss.Width(sessionSeg)+lipgloss.Width(modeSeg) {
		left += gitSeg + sessionSeg + modeSeg
	} else if budget >= lipgloss.Width(gitSeg)+lipgloss.Width(modeSeg) {
		left += gitSeg + modeSeg
	} else if budget >= lipgloss.Width(modeSeg) {
		left += modeSeg
	}
	space := max(0, m.width-lipgloss.Width(left)-rightW)
	return left + strings.Repeat(" ", space) + right
}

// boxWidth returns the lipgloss Width to pass to a bordered, padded box so
// that its total rendered width fits exactly within the viewport. lipgloss
// adds border on top of Width; padding is included in Width.
func boxWidth(m *Model) int {
	return max(1, m.width-2)
}

// wordWrap wraps text to a terminal cell width, preserving ANSI styles and
// existing newlines. Terminal cells matter here: CJK characters and emoji can
// occupy two columns even though they are a single rune.
func wordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, wrapLine(strings.TrimRight(line, " \t"), width)...)
	}
	return strings.Join(out, "\n")
}

// wrapLine wraps a single line (no embedded newlines) by grapheme display
// width. ansi.Wrap also keeps escape sequences intact when styled strings are
// passed by status, session, or tool renderers.
func wrapLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	return strings.Split(ansi.GraphemeWidth.Wrap(line, width, " "), "\n")
}

// renderGitSegment renders the branch and dirty-file counts for the header.
func (m *Model) renderGitSegment() string {
	if !m.gitStatus.Available {
		return ""
	}
	branch := gitBranchStyle.Render(" " + m.gitStatus.Branch)
	if !m.gitStatus.IsDirty() {
		return branch + gitCleanStyle.Render(" ✓")
	}
	parts := []string{branch}
	if m.gitStatus.Staged > 0 {
		parts = append(parts, gitStagedStyle.Render(fmt.Sprintf(" +%d", m.gitStatus.Staged)))
	}
	if m.gitStatus.Modified > 0 {
		parts = append(parts, gitModifiedStyle.Render(fmt.Sprintf(" ~%d", m.gitStatus.Modified)))
	}
	if m.gitStatus.Untracked > 0 {
		parts = append(parts, gitUntrackedStyle.Render(fmt.Sprintf(" ?%d", m.gitStatus.Untracked)))
	}
	return strings.Join(parts, "")
}

// renderContextSegment renders an approximate token usage indicator.
func (m *Model) renderContextSegment() string {
	if m.runtime == nil {
		return dimStyle.Render("ctx 0%")
	}
	tokens := m.runtime.ContextEstimate()
	pct := contextPercent(tokens, m.cfg.Provider.EffectiveContextWindow())
	return renderContextBar(pct)
}

func (m *Model) renderInputBar() string {
	// border(2) + padding(2) sit on top of Width, so subtract 2 to fit.
	return m.inputBorderStyle().Width(boxWidth(m)).Render(m.promptStyleM().Render("❯") + " " + m.input.View())
}

func (m *Model) renderKeyPrompt() string {
	w := boxWidth(m)
	label := keyLabel.Render("DeepSeek API Key:")
	inner := max(1, w-4) // border(2) + padding(2)
	content := wordWrap(label+" "+m.keyInput.View(), inner)
	return permBox.Width(w).Render(content)
}

func (m *Model) renderPermissionPrompt() string {
	if m.pendingPerm == nil {
		return ""
	}
	call := m.pendingPerm.call
	w := boxWidth(m)
	inner := max(1, w-4) // border(2) + padding(2)
	content := toolName.Render(call.Name) + "\n" +
		dimStyle.Render(wordWrap(extractToolDetail(call), inner)) + "\n" +
		permAsk.Render("allow? [y/N]")
	return permBox.Width(w).Render(content)
}

func (m *Model) renderHelpBar() string {
	if m.state == stateRunning {
		return spinnerStyle.Render(m.spinner.View()) + " " + dimStyle.Render("thinking...  ctrl+c to interrupt")
	}
	if m.state == stateCompacting {
		return spinnerStyle.Render(m.spinner.View()) + " " + dimStyle.Render("compacting...  ctrl+c to interrupt")
	}
	// Adapt the command hint to the available width so it never overflows
	// or garbles on narrow terminals.
	full := "/help /version /tools /status /session /newsession /compact /key /name /quit"
	right := "↑↓ scroll · tab mode"
	fullW := lipgloss.Width(full)
	rightW := lipgloss.Width(right)
	switch {
	case m.width >= fullW+rightW+2:
		space := m.width - fullW - rightW
		return dimStyle.Render(full) + strings.Repeat(" ", space) + dimStyle.Render(right)
	case m.width >= fullW+2:
		return dimStyle.Render(full)
	case m.width >= 26:
		return dimStyle.Render("/help · /quit · tab mode")
	default:
		return dimStyle.Render("/help · /quit")
	}
}

func (m *Model) renderUserMessage(text string) string {
	contentWidth := max(1, m.width-2)
	wrapped := wordWrap(text, contentWidth-2) // -2 for the left padding
	return m.userLabelStyle().Render("▸ "+m.cfg.User) + "\n" + userText.Width(contentWidth).Render(wrapped)
}

func (m *Model) renderAssistantMessage(text string) string {
	contentWidth := max(1, m.width-2)
	rendered := renderAssistantMarkdown(text, max(1, contentWidth-2)) // -2 for the left padding
	return assistantLabel.Render("◂ "+m.cfg.Assistant) + "\n" + assistantText.Width(contentWidth).Render(rendered)
}

func (m *Model) renderToolCall(call *agent.ToolCall) string {
	w := boxWidth(m)
	detailWidth := max(1, w-4) // border(2) + padding(2)
	name := wordWrap(call.Name, detailWidth)
	detail := wordWrap(extractToolDetail(*call), detailWidth)
	return toolBox.Width(w).Render(toolName.Render(name) + "\n" + dimStyle.Render(detail))
}

func (m *Model) renderTools() string {
	if m.runtime == nil {
		return dimStyle.Render("no runtime")
	}
	var lines []string
	lines = append(lines, toolName.Render("tools:"))
	for _, t := range m.runtime.Tools() {
		nameCol := fmt.Sprintf("  %-12s ", t.Name)
		nameW := lipgloss.Width(nameCol)
		descW := max(1, m.width-nameW)
		for _, wl := range wrapLine(t.Description, descW) {
			lines = append(lines, nameCol+wl)
			nameCol = strings.Repeat(" ", nameW)
		}
	}
	w := max(1, m.width)
	return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderStatus() string {
	var lines []string
	lines = append(lines, toolName.Render("status:"))
	if m.gitStatus.Available {
		lines = append(lines, "  "+cmdStyle.Render("git")+"  branch: "+m.gitStatus.Branch)
		lines = append(lines, fmt.Sprintf("  staged: %d  modified: %d  untracked: %d",
			m.gitStatus.Staged, m.gitStatus.Modified, m.gitStatus.Untracked))
		if !m.gitStatus.IsDirty() {
			lines = append(lines, "  "+gitCleanStyle.Render("working tree clean"))
		}
	} else {
		lines = append(lines, "  "+dimStyle.Render("not a git repository"))
	}
	if m.runtime != nil {
		tokens := m.runtime.ContextEstimate()
		window := m.cfg.Provider.EffectiveContextWindow()
		pct := contextPercent(tokens, window)
		ctxLine := fmt.Sprintf("  %s  ~%s tokens  %.0f%% of %s  (%d messages)",
			cmdStyle.Render("ctx"), formatTokens(tokens), pct, formatTokens(window), len(m.runtime.Messages()))
		for _, wl := range wrapLine(ctxLine, m.width) {
			lines = append(lines, wl)
		}
	}
	w := max(1, m.width)
	return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderHelp() string {
	return toolName.Render("mini-opencode") + "\n" +
		dimStyle.Render("  a fresh Go agent terminal") + "\n\n" +
		"  " + cmdStyle.Render("/help") + "    show this help\n" +
		"  " + cmdStyle.Render("/version") + " show version\n" +
		"  " + cmdStyle.Render("/tools") + "   list registered tools\n" +
		"  " + cmdStyle.Render("/status") + "  show git status and context usage\n" +
		"  " + cmdStyle.Render("/session") + "  list and switch to a saved conversation\n" +
		"  " + cmdStyle.Render("/newsession") + "  start a new conversation\n" +
		"  " + cmdStyle.Render("/archive") + " archive current conversation to JSON\n" +
		"  " + cmdStyle.Render("/compact") + " summarize and replace the conversation context\n" +
		"  " + cmdStyle.Render("/key") + "     set DeepSeek API key\n" +
		"  " + cmdStyle.Render("/name") + "    set or show user/assistant display names\n" +
		"  " + cmdStyle.Render("/quit") + "    exit"
}

// renderNames renders the current user/assistant display names for /name.
func (m *Model) renderNames() string {
	return toolName.Render("names:") + "\n" +
		"  user:      " + m.userLabelStyle().Render(m.cfg.User) + "\n" +
		"  assistant: " + assistantLabel.Render(m.cfg.Assistant) + "\n" +
		dimStyle.Render("  /name user <name> | /name assistant <name> | /name <name>")
}

// renderCommandMenu renders the slash-command autocomplete overlay.
func (m *Model) renderCommandMenu() string {
	var lines []string
	lines = append(lines, keyLabel.Render("commands:")+"  "+dimStyle.Render("↑↓ select · tab to complete · enter to run · esc to dismiss"))
	for i, c := range m.commandFiltered {
		marker := "  "
		name := cmdStyle.Render(c.Name)
		desc := dimStyle.Render(c.Desc)
		if i == m.commandCursor {
			marker = "▶ "
			name = toolName.Render(c.Name)
		}
		pad := max(1, 14-len(c.Name))
		lines = append(lines, marker+name+strings.Repeat(" ", pad)+desc)
	}
	w := boxWidth(m)
	inner := max(1, w-4) // border(2) + padding(2)
	// Wrap the header hint and each entry's description to the inner width.
	for i, line := range lines {
		lines[i] = wordWrap(line, inner)
	}
	return commandBox.Width(w).Render(strings.Join(lines, "\n"))
}

// renderModeSegment renders the current interaction mode badge in the header.
func (m *Model) renderModeSegment() string {
	label := fmt.Sprintf(" [%s]", m.mode)
	style := lipgloss.NewStyle().Bold(true)
	if m.mode == ModePlan {
		return style.Foreground(colorBlue).Render(label)
	}
	return style.Foreground(colorOrange).Render(label)
}

// inputBorderStyle returns the input border color for the current mode.
func (m *Model) inputBorderStyle() lipgloss.Style {
	c := colorOrange
	if m.mode == ModePlan {
		c = colorBlue
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c).Padding(0, 1)
}

// promptStyleM returns the prompt glyph style for the current mode.
func (m *Model) promptStyleM() lipgloss.Style {
	c := colorOrange
	if m.mode == ModePlan {
		c = colorBlue
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true)
}

// userLabelStyle returns the user message label style for the current mode.
func (m *Model) userLabelStyle() lipgloss.Style {
	c := colorOrange
	if m.mode == ModePlan {
		c = colorBlue
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true)
}

func extractToolDetail(call agent.ToolCall) string {
	var args map[string]any
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return string(call.Arguments)
	}
	for _, key := range []string{"command", "path", "file_path", "pattern", "query", "content"} {
		if v, ok := args[key]; ok {
			return fmt.Sprintf("%s: %v", key, v)
		}
	}
	var parts []string
	count := 0
	for k, v := range args {
		parts = append(parts, fmt.Sprintf("%s: %v", k, v))
		count++
		if count >= 3 {
			break
		}
	}
	return strings.Join(parts, "  ")
}

// formatTokens renders a token count in a human-friendly compact form.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1000000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
}

// contextPercent returns the context usage as a percentage of window.
func contextPercent(used, window int) float64 {
	if window <= 0 {
		return 0
	}
	return float64(used) / float64(window) * 100
}

// renderContextBar renders the context percentage with a compact bar and
// color-coded by usage tier (green < 60%, yellow < 85%, red otherwise).
func renderContextBar(pct float64) string {
	label := fmt.Sprintf("ctx %.0f%%", pct)
	switch {
	case pct < 60:
		return ctxLowStyle.Render(label)
	case pct < 85:
		return ctxMidStyle.Render(label)
	default:
		return ctxHighStyle.Render(label)
	}
}

// renderWorkspace builds the /workspace panel: the working directory and any
// additional allowed roots the agent may access outside it.
func (m *Model) renderWorkspace() string {
	var lines []string
	lines = append(lines, toolName.Render("workspace:"))
	w := max(1, m.width)
	rootPrefix := "  " + cmdStyle.Render("root") + "  "
	rootPrefixW := lipgloss.Width(rootPrefix)
	for _, wl := range wrapLine(m.workingDir, max(1, w-rootPrefixW)) {
		lines = append(lines, rootPrefix+wl)
		rootPrefix = strings.Repeat(" ", rootPrefixW)
	}
	roots := m.cfg.Workspace.AllowedRoots
	if len(roots) == 0 {
		lines = append(lines, "  "+dimStyle.Render("allowed roots: (none)"))
	} else {
		lines = append(lines, "  "+cmdStyle.Render("allowed")+":")
		bullet := "    - "
		bulletW := lipgloss.Width(bullet)
		for _, root := range roots {
			prefix := bullet
			for _, wl := range wrapLine(root, max(1, w-bulletW)) {
				lines = append(lines, prefix+wl)
				prefix = strings.Repeat(" ", bulletW)
			}
		}
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
}
