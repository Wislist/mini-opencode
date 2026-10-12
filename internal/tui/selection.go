package tui

import (
	"charm.land/lipgloss/v2"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

// Mouse text selection.
//
// The program enables mouse reporting (tea.WithMouseCellMotion) so wheel events
// arrive as exact mouse messages instead of being translated by the terminal.
// The cost is that the terminal stops doing its own drag-to-select: every press
// and motion is delivered to us, so "select some output and copy it" — which
// used to be the terminal's job — has to be implemented here.
//
// Transcript selections use rendered content rows and terminal cell columns,
// so scrolling moves the highlight with its text. Other UI selections use screen
// coordinates. Copying uses the same renderer, including offscreen transcript rows. The copied string goes to the
// system clipboard through the host clipboard API.

// selPoint is a position in terminal cells, 0-based.
type selPoint struct {
	row int
	col int
}

// selection is one drag. start and end are stored as dragged, and normalized
// for reading so a right-to-left or bottom-to-top drag works the same way.
type selection struct {
	active     bool
	transcript bool
	holding    bool
	pointer    selPoint
	start      selPoint
	end        selPoint
	// dragged distinguishes a click from a drag: releasing without moving
	// clears the selection instead of copying a single character.
	dragged bool
}

// normalizeSelection orders two drag points into a start/end pair, so callers
// never have to care which direction the user dragged.
func normalizeSelection(a, b selPoint) selection {
	if a.row > b.row || (a.row == b.row && a.col > b.col) {
		a, b = b, a
	}
	return selection{active: true, start: a, end: b, dragged: true}
}

// ClipboardWriter receives the text to place on the system clipboard.
type ClipboardWriter func(text string) error

// systemClipboard writes Unicode text through the native macOS or Windows
// clipboard implementation selected by the clipboard package.
func systemClipboard(text string) error {
	return clipboard.WriteAll(text)
}

// SetClipboardWriter overrides how copied text reaches the clipboard. Tests use
// it to capture the payload; passing nil restores the native clipboard writer.
func (m *Model) SetClipboardWriter(w ClipboardWriter) {
	if w == nil {
		m.clipboard = systemClipboard
		return
	}
	m.clipboard = w
}

// clearSelection drops the current selection.
func (m *Model) clearSelection() {
	m.selection = selection{}
	m.selectionScrollGeneration++
	m.selectionScrollPending = false
}

// selectedText returns the text under the current selection, or "" when there
// is none.
func (m *Model) selectedText() string {
	if !m.selection.active {
		return ""
	}
	sel := normalizeSelection(m.selection.start, m.selection.end)
	sel.transcript = m.selection.transcript
	return m.selectionText(sel)
}

// selectionText extracts the selected text from the rendered frame.
//
// It renders the frame, strips styling, and slices the requested cells. Working
// from the rendered output (rather than from m.blocks) keeps the result exactly
// what is on screen, including wrapped lines, without re-implementing the
// layout rules.
func (m *Model) selectionText(sel selection) string {
	if !sel.active || m.width <= 0 || m.height <= 0 {
		return ""
	}
	var lines []string
	if sel.transcript {
		// Render through the viewport itself so wrapping, padding and ANSI handling
		// agree with the visible transcript, including rows now off screen.
		viewport := m.viewport
		viewport.SetHeight(max(1, viewport.TotalLineCount()))
		viewport.SetYOffset(0)
		lines = screenLines(viewport.View())
	} else {
		lines = screenLines(m.renderFrame())
	}
	start, end := sel.start, sel.end
	start.row = clampInt(start.row, 0, max(0, len(lines)-1))
	end.row = clampInt(end.row, 0, max(0, len(lines)-1))

	var out []string
	for row := start.row; row <= end.row; row++ {
		if row < 0 || row >= len(lines) {
			continue
		}
		line := lines[row]
		from, to := 0, ansi.StringWidth(line)
		if row == start.row {
			from = clampInt(start.col, 0, ansi.StringWidth(line))
		}
		if row == end.row {
			to = clampInt(end.col, 0, ansi.StringWidth(line))
		}
		if from > to {
			from, to = to, from
		}
		out = append(out, strings.TrimRight(ansi.Cut(line, from, to), " "))
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// screenLines renders the frame and splits it into plain-text lines with all
// escape sequences removed and every line padded to the terminal width.
//
// Padding matters: a drag past the end of a short line should select the
// newline, not produce a ragged column mapping.
func screenLines(view string) []string {
	if view == "" {
		return nil
	}
	raw := strings.Split(view, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, ansi.Strip(line))
	}
	return lines
}

// highlightSelection overlays the selection onto an already rendered frame.
//
// The frame is re-styled per row rather than being rebuilt, so the underlying
// characters never change — only their appearance. Rows outside the selection
// are returned untouched.
func (m *Model) highlightSelection(view string) string {
	// Reset first: every early return below means "this frame has no
	// highlight", and leaving the flag set would make callers believe a
	// cleared selection is still painted.
	m.selectionHighlighted = false
	if !m.selection.active || view == "" {
		return view
	}
	sel := normalizeSelection(m.selection.start, m.selection.end)
	if sel.start == sel.end && !m.selection.dragged {
		return view
	}

	raw := strings.Split(view, "\n")
	if len(raw) == 0 {
		return view
	}
	first, last := 0, len(raw)-1
	if m.selection.transcript {
		first, last = m.selectionViewportBounds()
		shift := first - m.viewport.YOffset()
		sel.start.row += shift
		sel.end.row += shift
	}
	startRow := max(first, sel.start.row)
	endRow := min(last, sel.end.row)
	selected := false

	for row := startRow; row <= endRow; row++ {
		plain := ansi.Strip(raw[row])
		width := ansi.StringWidth(plain)
		if width == 0 {
			continue
		}
		from, to := 0, width
		if row == sel.start.row {
			from = clampInt(sel.start.col, 0, width)
		}
		if row == sel.end.row {
			to = clampInt(sel.end.col, 0, width)
		}
		if from > to {
			from, to = to, from
		}
		if from == to {
			continue
		}
		// The text outside the selection must survive: replacing the whole row
		// with the selected slice silently deleted everything before the drag
		// start and after the drag end.
		raw[row] = ansi.Cut(raw[row], 0, from) +
			selectionStyle.Render(ansi.Cut(plain, from, to)) +
			ansi.Cut(raw[row], to, width)
		selected = true
	}
	// Recorded so the rest of the program (and tests) can tell a highlighted
	// frame from a plain one: lipgloss strips styling when the output is not a
	// TTY, so the presence of escape codes is not a reliable signal.
	m.selectionHighlighted = selected
	return strings.Join(raw, "\n")
}

// handleSelectionMouse processes press/motion/release for the left button,
// reporting whether the event belonged to the selection interaction.
//
// Only the left button participates: wheel events stay with the scroll handler
// and other buttons are ignored so terminal-specific bindings keep working.
func (m *Model) handleSelectionMouse(msg tea.MouseMsg) bool {
	switch ev := msg.(type) {
	case tea.MouseClickMsg:
		if ev.Button != tea.MouseLeft {
			return false
		}
		m.clearSelection()
		top, bottom := m.selectionViewportBounds()
		point := selPoint{row: ev.Y, col: ev.X}
		m.selection = selection{active: true, holding: true, transcript: ev.Y >= top && ev.Y <= bottom, pointer: point}
		m.selection.start = m.selectionPoint(point)
		m.selection.end = m.selection.start
		return true
	case tea.MouseMotionMsg:
		if !m.selection.active || !m.selection.holding {
			return false
		}
		m.selection.pointer = selPoint{row: ev.Y, col: ev.X}
		m.selection.end = m.selectionPoint(m.selection.pointer)
		if m.selection.end != m.selection.start {
			m.selection.dragged = true
		}
		return true
	case tea.MouseReleaseMsg:
		if ev.Button != tea.MouseLeft || !m.selection.active || !m.selection.holding {
			return false
		}
		m.selection.holding = false
		m.selectionScrollGeneration++
		m.selectionScrollPending = false
		if !m.selection.dragged {
			// A plain click is a dismissal, not a one-character copy.
			m.clearSelection()
			return true
		}
		return m.copySelection()
	}
	return false
}

// copySelection sends the selected text to the clipboard and reports whether
// anything was copied.
func (m *Model) copySelection() bool {
	text := m.selectedText()
	if strings.TrimSpace(text) == "" {
		m.clearSelection()
		return true
	}
	writer := m.clipboard
	if writer == nil {
		writer = systemClipboard
	}
	// A clipboard failure must not break the UI: the selection stays visible so
	// the user can retry, and the error is surfaced in the transcript. Success
	// stays silent so copying never perturbs the transcript or scroll position.
	if err := writer(text); err != nil {
		m.addBlock(errorStyle.Render("✗ copy failed: " + err.Error()))
		m.refreshViewport()
	} else {
		m.markBlocksChanged()
	}
	return true
}

// itoa avoids pulling strconv in for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Transcript points use rendered content rows, while other UI selections keep
// screen coordinates. Header/footer rows never enter a transcript selection.
func (m *Model) selectionViewportBounds() (int, int) {
	top := lipgloss.Height(m.renderHeader())
	return top, min(m.height-1, top+m.viewport.Height()-1)
}

func (m *Model) selectionPoint(p selPoint) selPoint {
	p.col = clampInt(p.col, 0, max(0, m.width))
	if m.selection.transcript {
		top, bottom := m.selectionViewportBounds()
		p.row = clampInt(p.row, top, max(top, bottom)) - top + m.viewport.YOffset()
	}
	return p
}

type selectionScrollMsg struct{ generation uint64 }

func (m *Model) selectionScrollDirection() int {
	if !m.selection.active || !m.selection.holding || !m.selection.transcript || !m.selection.dragged {
		return 0
	}
	top, bottom := m.selectionViewportBounds()
	if bottom <= top {
		return 0
	}
	if m.selection.pointer.row <= top && !m.viewport.AtTop() {
		return -1
	}
	if m.selection.pointer.row >= bottom && !m.viewport.AtBottom() {
		return 1
	}
	return 0
}

func (m *Model) scheduleSelectionScroll() tea.Cmd {
	if m.selectionScrollPending || m.selectionScrollDirection() == 0 {
		return nil
	}
	m.selectionScrollPending = true
	generation := m.selectionScrollGeneration
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return selectionScrollMsg{generation} })
}

func (m *Model) handleSelectionScroll(msg selectionScrollMsg) tea.Cmd {
	if msg.generation != m.selectionScrollGeneration {
		return nil
	}
	m.selectionScrollPending = false
	direction := m.selectionScrollDirection()
	if direction == 0 {
		return nil
	}
	m.scrollLines(direction)
	return m.scheduleSelectionScroll()
}
