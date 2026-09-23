package tui

import (
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
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
// Selection is modelled in screen cells and resolved against the rendered frame,
// so the extracted text is what the user actually sees: no ANSI escapes, no box
// borders, no padding beyond the selection. The copied string goes to the
// system clipboard through pbcopy.

// selPoint is a position in terminal cells, 0-based.
type selPoint struct {
	row int
	col int
}

// selection is one drag. start and end are stored as dragged, and normalized
// for reading so a right-to-left or bottom-to-top drag works the same way.
type selection struct {
	active bool
	start  selPoint
	end    selPoint
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

// pbcopyClipboard writes to the macOS pasteboard. The project targets macOS
// only, so pbcopy is always present; a failure is reported to the caller rather
// than being silently swallowed.
func pbcopyClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// SetClipboardWriter overrides how copied text reaches the clipboard. Tests use
// it to capture the payload; passing nil restores the real pbcopy writer.
func (m *Model) SetClipboardWriter(w ClipboardWriter) {
	if w == nil {
		m.clipboard = pbcopyClipboard
		return
	}
	m.clipboard = w
}

// clearSelection drops the current selection.
func (m *Model) clearSelection() {
	m.selection = selection{}
}

// selectedText returns the text under the current selection, or "" when there
// is none.
func (m *Model) selectedText() string {
	if !m.selection.active {
		return ""
	}
	return m.selectionText(normalizeSelection(m.selection.start, m.selection.end))
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
	lines := screenLines(m.renderFrame())
	start, end := sel.start, sel.end
	start.row = clampInt(start.row, 0, max(0, len(lines)-1))
	end.row = clampInt(end.row, 0, max(0, len(lines)-1))

	var out []string
	for row := start.row; row <= end.row; row++ {
		if row < 0 || row >= len(lines) {
			continue
		}
		line := lines[row]
		from, to := 0, len(line)
		if row == start.row {
			from = clampInt(start.col, 0, len(line))
		}
		if row == end.row {
			to = clampInt(end.col, 0, len(line))
		}
		if from > to {
			from, to = to, from
		}
		out = append(out, strings.TrimRight(line[from:to], " "))
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
	startRow := clampInt(sel.start.row, 0, len(raw)-1)
	endRow := clampInt(sel.end.row, 0, len(raw)-1)
	selected := false

	for row := startRow; row <= endRow; row++ {
		plain := ansi.Strip(raw[row])
		runes := []rune(plain)
		if len(runes) == 0 {
			continue
		}
		from, to := 0, len(runes)
		if row == sel.start.row {
			from = clampInt(sel.start.col, 0, len(runes))
		}
		if row == sel.end.row {
			to = clampInt(sel.end.col, 0, len(runes))
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
		raw[row] = string(runes[:from]) +
			selectionStyle.Render(string(runes[from:to])) +
			string(runes[to:])
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
		m.selection = selection{active: true, start: selPoint{row: ev.Y, col: ev.X}, end: selPoint{row: ev.Y, col: ev.X}}
		return true
	case tea.MouseMotionMsg:
		if !m.selection.active {
			return false
		}
		m.selection.end = selPoint{row: ev.Y, col: ev.X}
		if m.selection.end != m.selection.start {
			m.selection.dragged = true
		}
		return true
	case tea.MouseReleaseMsg:
		if !m.selection.active {
			return false
		}
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
		writer = pbcopyClipboard
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
