package tui

import (
	"io"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// NativeCursorPosition returns the terminal cursor position that corresponds to
// the focused text input. Bubble Tea renders its own visual cursor and then
// leaves the real terminal cursor on the last line of the frame; terminal IMEs
// use that real cursor to place pre-edit text. The app output writer uses this
// position to move the native cursor back into the active input box after each
// render.
func (m *Model) NativeCursorPosition() (col, row int, ok bool) {
	m.nativeCursorMu.RLock()
	defer m.nativeCursorMu.RUnlock()
	return m.nativeCursorCol, m.nativeCursorRow, m.nativeCursorOK
}

func (m *Model) setNativeCursorPosition(col, row int, ok bool) {
	m.nativeCursorMu.Lock()
	defer m.nativeCursorMu.Unlock()
	m.nativeCursorCol = col
	m.nativeCursorRow = row
	m.nativeCursorOK = ok
}

func (m *Model) updateNativeCursorPosition(header string, footer []string) {
	col, row, ok := m.computeNativeCursorPosition(header, footer)
	m.setNativeCursorPosition(col, row, ok)
}

func (m *Model) computeNativeCursorPosition(header string, footer []string) (col, row int, ok bool) {
	if m.width <= 0 || m.height <= 0 {
		return 0, 0, false
	}

	baseRow := lipgloss.Height(header) + m.viewport.Height

	switch m.state {
	case stateIdle:
		preFooterHeight := 0
		if m.commandMenuOpen() && len(m.commandFiltered) > 0 && len(footer) > 0 {
			preFooterHeight = lipgloss.Height(footer[0])
		}
		// Input bar has a top border, then the editable content row.
		row = baseRow + preFooterHeight + 2
		col = inputBarCursorColumn(m.input)
		return clampCursor(col, row, m.width, m.height)
	case stateKeyPrompt:
		// Key prompt has a top border, then the content row.
		row = baseRow + 2
		col = keyPromptCursorColumn(m.keyInput)
		return clampCursor(col, row, m.width, m.height)
	default:
		return 0, 0, false
	}
}

func inputBarCursorColumn(input textinput.Model) int {
	// renderInputBar lays out: left border, left padding, "❯", space,
	// textinput view. CUP coordinates are 1-based, so add one more cell to land
	// on the cursor cell after the already-rendered prefix.
	prefixWidth := 1 + 1 + lipgloss.Width("❯ ") + visibleCursorPrefixWidth(input)
	return prefixWidth + 1
}

func keyPromptCursorColumn(input textinput.Model) int {
	// renderKeyPrompt lays out: left border, left padding, label, space,
	// textinput view.
	prefixWidth := 1 + 1 + lipgloss.Width("DeepSeek API Key: ") + visibleCursorPrefixWidth(input)
	return prefixWidth + 1
}

func visibleCursorPrefixWidth(input textinput.Model) int {
	value := []rune(input.Value())
	pos := input.Position()
	if pos < 0 {
		pos = 0
	}
	if pos > len(value) {
		pos = len(value)
	}
	width := lipgloss.Width(string(value[:pos]))
	if input.Width > 0 && width >= input.Width {
		// textinput scrolls horizontally when the cursor moves past the visible
		// field. Its offset is private, but the cursor remains inside the field;
		// keep native IME pre-edit text there rather than letting it drift out.
		return max(0, input.Width-1)
	}
	return width
}

func clampCursor(col, row, maxCol, maxRow int) (int, int, bool) {
	if maxCol <= 0 || maxRow <= 0 {
		return 0, 0, false
	}
	if col < 1 {
		col = 1
	}
	if row < 1 {
		row = 1
	}
	if col > maxCol {
		col = maxCol
	}
	if row > maxRow {
		row = maxRow
	}
	return col, row, true
}

// CursorPositionFunc reports a 1-based terminal cursor position.
type CursorPositionFunc func() (col, row int, ok bool)

// NewNativeCursorWriter wraps Bubble Tea's output and appends a cursor move
// after frame renders so terminal IME pre-edit text appears in the active input
// field instead of on Bubble Tea's final help/status line.
func NewNativeCursorWriter(out io.Writer, cursor CursorPositionFunc) io.Writer {
	return &nativeCursorWriter{out: out, cursor: cursor}
}

type nativeCursorWriter struct {
	out    io.Writer
	cursor CursorPositionFunc
}

func (w *nativeCursorWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	if err != nil {
		return n, err
	}
	if endsWithRendererCursorPosition(p) && w.cursor != nil {
		if col, row, ok := w.cursor(); ok {
			if _, err := io.WriteString(w.out, ansi.CursorPosition(col, row)); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

func endsWithRendererCursorPosition(p []byte) bool {
	// Bubble Tea's alternate-screen renderer ends frames with CUP using an empty
	// column: ESC [ <row> ; H. Match only that exact suffix so ordinary terminal
	// control sequences are not followed by our cursor move.
	if len(p) < len("\x1b[1;H") || p[len(p)-1] != 'H' || p[len(p)-2] != ';' {
		return false
	}
	i := len(p) - 3
	hasRow := false
	for i >= 0 && p[i] >= '0' && p[i] <= '9' {
		hasRow = true
		i--
	}
	return hasRow && i >= 1 && p[i] == '[' && p[i-1] == '\x1b'
}
