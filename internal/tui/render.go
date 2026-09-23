package tui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/agent/tools"
)

// ── View ──────────────────────────────────────────────

func (m *Model) View() tea.View {
	v := tea.NewView(m.renderFrame())
	// In Bubble Tea v2 the alternate screen and mouse reporting are declared
	// per view rather than as program options. Cell motion (rather than all
	// motion) keeps drag reporting quiet so text selection still works with the
	// terminal's own selection modifier.
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// renderFrame builds the complete screen as a string.
//
// It is kept separate from View so the rest of the program — selection
// extraction, layout tests — can work with plain text, and View stays a thin
// adapter over it.
func (m *Model) renderFrame() string {
	if m.width == 0 {
		return "loading..."
	}

	header := m.renderHeader()
	footer := m.renderFooter()
	m.fitViewport(header, footer)
	m.updateNativeCursorPosition(header, footer)

	sections := make([]string, 0, len(footer)+2)
	sections = append(sections, header, m.viewport.View())
	// Drop whole optional sections until the frame fits. The prompt box grows
	// with its content, so in a short terminal the fixed parts can exceed the
	// window; trimming the assembled string instead would cut a box open.
	sections = append(sections, m.fitFooterSections(footer)...)
	frame := lipgloss.JoinVertical(lipgloss.Left, sections...)
	// The selection is painted onto the finished frame so the characters are
	// never altered — only their styling — and the extracted text matches what
	// the user sees.
	return m.highlightSelection(frame)
}

// renderFooter returns every section below the transcript viewport. Keeping
// these sections together lets fitViewport reserve their actual rendered
// height instead of relying on a fixed subtraction that leaves stale rows.
//
// The result is memoized against footerDirty. View() runs on every message —
// including each spinner tick — and rebuilding the todo panel, input bar and
// help bar plus measuring every section with lipgloss was measurable work per
// frame, even though none of those sections change between ticks.
func (m *Model) renderFooter() []string {
	if !m.footerDirty && m.cachedFooter != nil {
		return m.cachedFooter
	}
	sections := m.buildFooter()
	m.cachedFooter = sections
	m.footerDirty = false
	return sections
}

// invalidateFooter marks the memoized footer stale. It must be called whenever
// a field the footer renders changes: the app state, the todo list, the input
// value, the mode, the session list, or the window size.
func (m *Model) invalidateFooter() {
	m.footerDirty = true
}

// footerSection identifies each section of the footer so callers can measure the
// area above the input bar without re-deriving the layout. Detection by content
// is not reliable: lipgloss drops border color when it decides no color profile
// is available, which left the input bar indistinguishable from the todo panel.
type footerSection int

const (
	footerSectionOther footerSection = iota
	footerSectionInputBar
)

// footerKinds lists one entry per footer section, in render order. It must stay
// in lockstep with buildFooter, which is why both are derived in one place.
func (m *Model) footerKinds() []footerSection {
	kinds := make([]footerSection, 0, 4)
	switch {
	case m.state == statePermission && m.pendingPerm != nil:
		kinds = append(kinds, footerSectionOther)
	case m.state == statePlanApproval && m.pendingPlan != nil:
		kinds = append(kinds, footerSectionOther)
	case m.state == stateKeyPrompt:
		kinds = append(kinds, footerSectionOther)
	case m.state == stateSessionList:
		kinds = append(kinds, footerSectionOther)
	default:
		if m.commandMenuOpen() && len(m.commandFiltered) > 0 {
			kinds = append(kinds, footerSectionOther)
		}
		if panel := m.renderTodoPanel(); panel != "" {
			kinds = append(kinds, footerSectionOther)
		}
		kinds = append(kinds, footerSectionInputBar)
	}
	return append(kinds, footerSectionOther)
}

// footerSectionsAboveInput sums the heights of the sections sitting above the
// input bar. kinds and footer come from footerKinds and renderFooter, which walk
// the same layout, so the count is exact even when the todo panel, the command
// menu, or both are present.
func footerSectionsAboveInput(footer []string, kinds []footerSection) int {
	height := 0
	for i, section := range footer {
		if i < len(kinds) && kinds[i] == footerSectionInputBar {
			break
		}
		height += lipgloss.Height(section)
	}
	return height
}

func (m *Model) buildFooter() []string {
	var sections []string
	if m.state == statePermission && m.pendingPerm != nil {
		sections = append(sections, m.renderPermissionPrompt())
	} else if m.state == statePlanApproval && m.pendingPlan != nil {
		sections = append(sections, m.renderPlanPrompt())
	} else if m.state == stateKeyPrompt {
		sections = append(sections, m.renderKeyPrompt())
	} else if m.state == stateSessionList {
		sections = append(sections, m.renderSessionList())
	} else {
		if m.commandMenuOpen() && len(m.commandFiltered) > 0 {
			sections = append(sections, m.renderCommandMenu())
		}
		// The todo panel sits directly above the input so the plan stays
		// visible while the agent works.
		if panel := m.renderTodoPanel(); panel != "" {
			sections = append(sections, panel)
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
	if m.viewport.Height() == height {
		return
	}

	wasAtBottom := m.viewport.AtBottom()
	m.viewport.SetHeight(height)
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

func (m *Model) renderHeader() string {
	left := headerStyle.Render(m.headerTitle())
	titleW := lipgloss.Width(left)
	// Progressively drop header segments as the terminal narrows so the
	// header never overflows or overlaps itself.
	gitSeg := m.renderGitSegment()
	modeSeg := m.renderModeSegment() + dimStyle.Render(" ["+m.PermissionMode().Label()+"]")
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

	// Budget for left-side segments after reserving the right side. The session
	// title is the only flexible part: it is shortened to whatever is left
	// instead of being dropped, because the title is what identifies the
	// conversation. The fixed segments are kept whole.
	budget := m.width - titleW - rightW - 2
	fixed := lipgloss.Width(gitSeg) + lipgloss.Width(modeSeg)
	switch {
	case budget >= fixed+minSessionTitleWidth:
		left += gitSeg + m.renderSessionSegment(budget-fixed) + modeSeg
	case budget >= fixed:
		left += gitSeg + modeSeg
	case budget >= lipgloss.Width(modeSeg):
		left += modeSeg
	}
	space := max(0, m.width-lipgloss.Width(left)-rightW)
	return left + strings.Repeat(" ", space) + right
}

// minSessionTitleWidth is the smallest useful slice of a session title: below
// this the ellipsis would carry more information than the title itself.
const minSessionTitleWidth = 8

// headerTitle is the product label, shortened when the terminal is too narrow
// to hold it.
//
// The label is the one segment that is always present, so on a very narrow
// terminal it must give way itself; otherwise it alone would overflow and wrap
// the single-row header.
func (m *Model) headerTitle() string {
	const full = "◆ mini-opencode"
	if m.width <= 0 || lipgloss.Width(full) <= m.width {
		return full
	}
	// Fall back to the shortest form that still identifies the program.
	for _, candidate := range []string{"◆ mini-opencode", "◆ mini", "◆"} {
		if lipgloss.Width(candidate) <= m.width {
			return candidate
		}
	}
	return ""
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

// renderContextSegment renders the context usage indicator.
//
// The percentage is of the whole prompt, because that is what the window limit
// applies to. The system prompt is a large fixed floor (tens of KB of template,
// tool instructions and AGENTS.md), so the indicator also shows how much of the
// total is the conversation: otherwise clearing the session with /newsession
// barely moves the number and looks like the reset failed.
func (m *Model) renderContextSegment() string {
	if m.runtime == nil {
		return dimStyle.Render("ctx 0%")
	}
	details := m.runtime.ContextDetails()
	pct := contextPercent(details.Total(), m.cfg.Provider.EffectiveContextWindow())
	return renderContextBar(pct, details.ConversationTokens)
}

func (m *Model) renderInputBar() string {
	// A terminal too short for the border falls back to a single row, so the
	// box cannot take more rows than exist and push the frame off screen.
	if !m.borderedInputFits() {
		return m.promptStyleM().Render("❯ ") + m.input.View()
	}
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
		dimStyle.Render(wordWrap(extractToolDetail(call), inner)) + "\n"
	if preview := toolDiffPreview(call, m.workingDir); preview != "" {
		content += renderDiffLines(preview, inner) + "\n"
	}
	content += permAsk.Render("allow? [y] once · [a] always this session · [n] deny")
	return permBox.Width(w).Render(content)
}

func (m *Model) renderHelpBar() string {
	if m.state == stateRunning {
		// Advertise the fold while running: that is when the task panel is most
		// likely to be in the way.
		hint := "thinking...  esc to interrupt"
		if strings.TrimSpace(m.todos) != "" {
			hint = "thinking...  esc to interrupt · ctrl+t todos"
		}
		return spinnerStyle.Render(m.spinner.View()) + " " + dimStyle.Render(hint)
	}
	if m.state == stateCompacting {
		return spinnerStyle.Render(m.spinner.View()) + " " + dimStyle.Render("compacting...  esc to interrupt")
	}
	// Adapt the command hint to the available width so it never overflows
	// or garbles on narrow terminals.
	//
	// The command list is deliberately an abbreviated selection rather than
	// every slash command: the full list is 76 columns, which leaves no room
	// for the key hints on a standard 80-column terminal, and the hints are
	// what users cannot discover any other way (/help lists the commands).
	full := "/help /tools /status /compact /session /quit"
	hints := []string{"↑↓ scroll", "shift+enter newline", "tab mode", "ctrl+t todos"}
	right := strings.Join(hints, " · ")
	fullW := lipgloss.Width(full)
	switch {
	case m.width >= fullW+lipgloss.Width(right)+2:
		space := m.width - fullW - lipgloss.Width(right)
		return dimStyle.Render(full) + strings.Repeat(" ", space) + dimStyle.Render(right)
	case m.width >= fullW+lipgloss.Width(right)+1:
		return dimStyle.Render(full) + " " + dimStyle.Render(right)
	}
	// Too narrow for both: keep the shortcuts, dropping the least useful first.
	for i := len(hints); i >= 1; i-- {
		candidate := strings.Join(hints[:i], " · ")
		if lipgloss.Width(candidate)+8 <= m.width {
			return dimStyle.Render("/help · " + candidate)
		}
	}
	return dimStyle.Render("/help")
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

// renderStreamingMessage renders the message currently being streamed, reusing
// the settled prefix cached on the model.
//
// A throttled repaint still has to run Glamour over whatever text it is handed,
// and that costs ~0.5MB/s (16KB ≈ 30ms on an M2), so without this a long answer
// stalls the frame no matter how well the deltas are coalesced. Only the
// trailing, still-growing block is re-rendered here; everything before it is
// settled and served from the cache. The output is byte-identical to
// renderAssistantMessage, which markdown_cache_test.go asserts.
func (m *Model) renderStreamingMessage(text string) string {
	contentWidth := max(1, m.width-2)
	mdWidth := max(1, contentWidth-2) // -2 for the left padding

	if m.streamCache == nil {
		m.streamCache = newMarkdownRenderCache()
	}
	rendered := m.streamCache.render(text, mdWidth)
	return assistantLabel.Render("◂ "+m.cfg.Assistant) + "\n" + assistantText.Width(contentWidth).Render(rendered)
}

func (m *Model) renderToolCall(call *agent.ToolCall) string {
	w := boxWidth(m)
	detailWidth := max(1, w-4) // border(2) + padding(2)
	name := wordWrap(call.Name, detailWidth)
	detail := wordWrap(extractToolDetail(*call), detailWidth)
	return toolBox.Width(w).Render(toolName.Render(name) + "\n" + dimStyle.Render(detail))
}

// toolResultMaxRows bounds how much tool output is shown. Tool results are the
// bulkiest thing in the transcript (a `read` returns a whole file), and the
// transcript is unbounded, so the box shows a window of the output instead of
// all of it. The marker tells the reader how much was hidden, so a partial view
// is never mistaken for the whole result.
const toolResultMaxRows = 12

// renderToolResult renders a finished tool result as a bordered box.
//
// It mirrors renderToolCall: the call and its output are two halves of the same
// unit, so both are boxed. Emitting the output as a bare "→ ..." line lost the
// output's own line structure and made code, diffs and file contents
// indistinguishable from prose.
func (m *Model) renderToolResult(result *agent.ToolResult) string {
	w := boxWidth(m)
	contentWidth := max(1, w-4) // border(2) + padding(2)

	marker, text := "→ ", result.Content
	if result.Error != "" {
		marker, text = "✗ ", result.Error
	}

	body := toolResultBody(text, contentWidth)
	if len(body) == 0 {
		body = []string{dimStyle.Render("(no output)")}
	}
	header := toolArrow.Render(marker)
	return toolBox.Width(w).Render(header + strings.Join(body, "\n"))
}

// toolResultBody wraps and caps tool output so it fits the box. Wrapping uses
// display width, so CJK output no longer breaks, and the truncation marker
// replaces the old raw byte slice that could cut a UTF-8 sequence in half.
//
// Only output that is entirely whitespace counts as "no output": a result made
// of blank lines is still something the tool returned, and reporting it as
// empty would hide that.
func toolResultBody(text string, width int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	wrapped := wordWrap(strings.TrimRight(text, "\n"), width)
	if strings.TrimSpace(wrapped) == "" {
		return nil
	}

	lines := strings.Split(wrapped, "\n")
	if len(lines) <= toolResultMaxRows {
		return lines
	}
	hidden := len(lines) - toolResultMaxRows
	lines = lines[:toolResultMaxRows]
	lines = append(lines, codeOmittedStyle.Render(fmt.Sprintf("⋯ (%d more lines)", hidden)))
	return lines
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
	lines = append(lines, fmt.Sprintf("  permissions: %s · %s", m.PermissionMode(), m.PermissionMode().Label()))
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
		details := m.runtime.ContextDetails()
		tokens := details.Total()
		window := m.cfg.Provider.EffectiveContextWindow()
		pct := contextPercent(tokens, window)
		source := "estimated"
		if details.Reported > 0 {
			source = "provider-reported prompt size"
		}
		ctxLine := fmt.Sprintf("  %s  %s tokens  %.0f%% of %s  (%d messages, %s)",
			cmdStyle.Render("ctx"), formatTokens(tokens), pct, formatTokens(window),
			len(m.runtime.Messages()), source)
		for _, wl := range wrapLine(ctxLine, m.width) {
			lines = append(lines, wl)
		}
		// Spell out the fixed floor. Without this the total barely moves when
		// the conversation is cleared, which reads as a broken reset rather
		// than as "the system prompt is most of what you are seeing".
		breakdownLine := fmt.Sprintf("  %s  %s system prompt + tools, %s conversation",
			dimStyle.Render("     "), formatTokens(details.SystemTokens),
			formatTokens(details.ConversationTokens))
		for _, wl := range wrapLine(breakdownLine, m.width) {
			lines = append(lines, wl)
		}
		if auto := m.runtime.AutoCompactions(); auto > 0 {
			lines = append(lines, fmt.Sprintf("  %s  %d automatic compaction(s) this session",
				cmdStyle.Render("compacted"), auto))
		}
		usage := m.runtime.Usage()
		if !usage.IsZero() {
			usageLine := fmt.Sprintf("  %s  prompt %s  completion %s  total %s",
				cmdStyle.Render("tokens"), formatTokens(usage.PromptTokens),
				formatTokens(usage.CompletionTokens), formatTokens(usage.TotalTokens))
			for _, wl := range wrapLine(usageLine, m.width) {
				lines = append(lines, wl)
			}
		} else {
			lines = append(lines, "  "+dimStyle.Render("tokens: provider reports no usage"))
		}
	}
	if allowed := m.SessionAllowedTools(); len(allowed) > 0 {
		lines = append(lines, "  "+cmdStyle.Render("allowed")+"  "+
			dimStyle.Render("session-approved tools: "+strings.Join(allowed, ", ")))
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
		"  " + cmdStyle.Render("/permissions") + "  帮我批准 / 完全访问 / 请求批准\n" +
		"  " + cmdStyle.Render("/mcp") + "     show MCP server status\n" +
		"  " + cmdStyle.Render("/init") + "    analyze the repo and write AGENTS.md\n" +
		"  " + cmdStyle.Render("/fork") + "    branch this conversation into a new session\n" +
		"  " + cmdStyle.Render("/plan") + "    toggle plan mode (read-only analysis)\n" +
		"  " + cmdStyle.Render("/undo") + "    restore the newest file snapshot\n" +
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

// inputBorderColor returns the border color of the input bar for the current
// mode.
func inputBorderColor(plan bool) color.Color {
	if plan {
		return colorBlue
	}
	return colorOrange
}

// inputBorderStyle returns the input border color for the current mode.
func (m *Model) inputBorderStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(inputBorderColor(m.mode == ModePlan)).
		Padding(0, 1)
}

// promptStyleM returns the prompt glyph style for the current mode.
func (m *Model) promptStyleM() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(inputBorderColor(m.mode == ModePlan)).Bold(true)
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

// renderContextBar renders the context indicator, color-coded by usage tier
// (green < 60%, yellow < 85%, red otherwise). The percentage is of the total
// prompt; conversationTokens is the part that is the actual conversation, shown
// so a cleared session reads as cleared rather than as an unchanged percentage.
func renderContextBar(pct float64, conversationTokens int) string {
	label := fmt.Sprintf("ctx %.0f%%", pct)
	if conversationTokens > 0 {
		label = fmt.Sprintf("ctx %.0f%% · %s chat", pct, formatTokens(conversationTokens))
	}
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

// renderMCPStatus lists configured MCP servers and their startup result.
func (m *Model) renderMCPStatus() string {
	if m.mcpStatus == nil {
		return dimStyle.Render("mcp: not configured")
	}
	lines := []string{toolName.Render("mcp:")}
	statuses := m.mcpStatus()
	if len(statuses) == 0 {
		lines = append(lines, "  "+dimStyle.Render("no MCP servers configured (config.json -> mcpServers)"))
	}
	for _, status := range statuses {
		for _, wl := range wrapLine("  "+status, max(1, m.width)) {
			lines = append(lines, wl)
		}
	}
	return strings.Join(lines, "\n")
}

// renderTodoPanel renders the agent's task list above the input bar.
//
// The panel is empty when there is no list. It collapses to a single summary
// line when the user folds it with ctrl+t: the point of folding is to stop the
// list consuming screen space, not to hide the fact that work is outstanding,
// so the progress counters stay visible.
func (m *Model) renderTodoPanel() string {
	if strings.TrimSpace(m.todos) == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(m.todos), "\n")
	w := boxWidth(m)
	inner := max(1, w-4)

	if m.todosCollapsed {
		return m.renderCollapsedTodoPanel(lines, w, inner)
	}

	rendered := make([]string, 0, len(lines)+1)
	rendered = append(rendered, keyLabel.Render("todos:"))
	for _, line := range lines {
		rendered = append(rendered, todoLineStyle(line).Render(line))
	}
	for i, line := range rendered {
		rendered[i] = wordWrap(line, inner)
	}
	return commandBox.Width(w).Render(strings.Join(rendered, "\n"))
}

// renderCollapsedTodoPanel renders the folded form: one line naming the list
// and how far along it is.
func (m *Model) renderCollapsedTodoPanel(lines []string, w, inner int) string {
	done, total, inProgress := todoCounts(lines)
	summary := fmt.Sprintf("%d/%d done", done, total)
	if inProgress != "" {
		summary += " · " + inProgress
	}
	line := keyLabel.Render("todos:") + " " + dimStyle.Render(summary) +
		dimStyle.Render("  (ctrl+t)")
	return commandBox.Width(w).Render(wordWrap(line, inner))
}

// todoCounts reads progress back off a rendered checklist. The trailing
// "(n/m done)" summary line is a count, not an entry, so it is skipped.
func todoCounts(lines []string) (done, total int, inProgress string) {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "(") {
			continue
		}
		total++
		switch {
		case strings.HasPrefix(trimmed, "[x]"):
			done++
		case strings.HasPrefix(trimmed, "[>]"):
			if inProgress == "" {
				inProgress = strings.TrimSpace(strings.TrimPrefix(trimmed, "[>]"))
			}
		}
	}
	return done, total, inProgress
}

// todoLineStyle picks the style for one rendered checklist entry.
func todoLineStyle(line string) lipgloss.Style {
	switch {
	case strings.HasPrefix(line, "[x]"):
		return gitCleanStyle
	case strings.HasPrefix(line, "[>]"):
		return permAsk
	default:
		return dimStyle
	}
}

// todoTextFromJSON renders a stored todo list, returning "" when empty.
func todoTextFromJSON(raw string) string {
	items := tools.ParseTodos(raw)
	if len(items) == 0 {
		return ""
	}
	return tools.RenderTodos(items)
}

// renderPlanPrompt shows a submitted plan and asks the user to approve it.
// The plan is capped so a long plan cannot push the input bar off screen; the
// full text is already in the transcript above.
func (m *Model) renderPlanPrompt() string {
	if m.pendingPlan == nil {
		return ""
	}
	w := boxWidth(m)
	inner := max(1, w-4)
	const maxPlanLines = 12

	planLines := strings.Split(strings.TrimSpace(m.pendingPlan.plan), "\n")
	shown := planLines
	truncated := 0
	if len(planLines) > maxPlanLines {
		shown = planLines[:maxPlanLines]
		truncated = len(planLines) - maxPlanLines
	}
	content := keyLabel.Render("plan ready for approval:") + "\n"
	for _, line := range shown {
		content += dimStyle.Render(wordWrap(line, inner)) + "\n"
	}
	if truncated > 0 {
		content += dimStyle.Render(fmt.Sprintf("… %d more lines (see the transcript)", truncated)) + "\n"
	}
	content += permAsk.Render("approve and start implementing? [y/N]")
	return permBox.Width(w).Render(strings.TrimRight(content, "\n"))
}

// renderDiffLines colorizes a diff preview: removals red, additions green,
// everything else dim.
func renderDiffLines(preview string, width int) string {
	var lines []string
	for _, line := range strings.Split(preview, "\n") {
		line = wordWrap(line, width)
		switch {
		case strings.HasPrefix(line, "+ "):
			lines = append(lines, toolAddedStyle.Render(line))
		case strings.HasPrefix(line, "- "):
			lines = append(lines, toolRemovedStyle.Render(line))
		default:
			lines = append(lines, dimStyle.Render(line))
		}
	}
	return strings.Join(lines, "\n")
}

// indentLines prefixes every line of text, for indenting a block under a
// one-line notice.
func indentLines(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// fitFooterSections returns the footer sections that fit in the terminal,
// dropping the least important whole sections first.
//
// The prompt box and the transcript are never dropped: they are what the user
// is interacting with. The help bar goes first, then the task panel.
func (m *Model) fitFooterSections(footer []string) []string {
	if m.height <= 0 || len(footer) == 0 {
		return footer
	}
	used := lipgloss.Height(m.renderHeader()) + m.viewport.Height()
	kept := make([]string, 0, len(footer))
	for i, section := range footer {
		h := lipgloss.Height(section)
		if used+h <= m.height {
			kept = append(kept, section)
			used += h
			continue
		}
		// Out of room. The last section is the help bar and the one before the
		// input bar is the task panel; both are droppable. The input bar itself
		// is kept even if it overflows, because losing it means losing typing.
		if m.isDroppableFooterSection(footer, i) {
			continue
		}
		kept = append(kept, section)
		used += h
	}
	return kept
}

// isDroppableFooterSection reports whether a footer section may be omitted when
// the frame does not fit.
func (m *Model) isDroppableFooterSection(footer []string, index int) bool {
	// The help bar is always last.
	if index == len(footer)-1 {
		return true
	}
	// The task panel sits immediately above the input bar when present.
	if index == len(footer)-2 && strings.TrimSpace(m.todos) != "" {
		panel := m.renderTodoPanel()
		return panel != "" && footer[index] == panel
	}
	return false
}
