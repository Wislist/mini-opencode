package tui

// isInputSelectAllKey keeps the platform conventions separate. Ctrl+A remains
// textarea's line-start command on macOS and Linux; Windows uses it for select
// all, while macOS terminals report Command+A as super+a when the enhanced
// keyboard protocol is available.
func isInputSelectAllKey(key, goos string) bool {
	switch goos {
	case "darwin":
		return key == "super+a"
	case "windows":
		return key == "ctrl+a"
	default:
		return false
	}
}

func (m *Model) cancelInputSelectAll() {
	m.inputSelectAll = false
}

func (m *Model) clearSelectedInput() {
	m.input.Reset()
	m.cancelInputSelectAll()
}

// Multiline prompt input.
//
// The prompt box is a textarea, so a long question can be typed across several
// lines: Enter sends, Ctrl+J and Alt+Enter insert a newline (see handleKey for
// why Shift+Enter cannot be used), and the box grows with its content up to
// maxInputBarLines before scrolling internally.

// minHeightForBorderedInput is the terminal height below which the prompt box
// gives up its border and collapses to a single row.
const minHeightForBorderedInput = 7

// maxInputBarLines bounds how tall the prompt box may grow.
//
// The box takes its rows out of the transcript, so an unbounded one would let a
// pasted document squeeze the conversation to nothing. Past this height the
// textarea scrolls its own content instead.
const maxInputBarLines = 6

// insertNewline adds a newline at the caret and resizes the box.
//
// The textarea's own InsertString is used rather than splicing the value: the
// widget tracks the caret as a column within the current row, so rebuilding the
// value and calling SetCursor inserted the newline at the wrong place. Letting
// the widget apply the edit keeps its own bookkeeping consistent.
func (m *Model) insertNewline() {
	m.input.InsertString("\n")
	m.syncInputHeight()
	m.invalidateFooter()
}

// syncInputHeight sizes the prompt box to its content.
//
// The clamp is the smaller of the content height and what the terminal can
// spare: a tall input in a short terminal would otherwise push the frame past
// the bottom of the screen, which is the overflow that used to make boxes look
// detached. A minimum of one row keeps the prompt usable even then.
func (m *Model) syncInputHeight() {
	// Let textarea use its own wrapping model to calculate both height and the
	// internal viewport offset. Growing a one-row textarea after a paste with a
	// standalone SetHeight leaves its viewport parked on the final row, which
	// produces a tall box containing one visible line and blank space.
	m.input.DynamicHeight = true
	m.input.MinHeight = 1
	m.input.MaxHeight = m.maxInputLines()
	// SetWidth triggers textarea's dynamic-height recalculation without changing
	// the value or caret. The prompt textarea has no internal border or prompt,
	// so Width is also the correct width to feed back here.
	m.input.SetWidth(max(1, m.input.Width()))
}

// maxInputLines is how many content rows the prompt box may use right now,
// bounded by both the feature cap and the room left in the terminal.
func (m *Model) maxInputLines() int {
	if m.height <= 0 {
		return maxInputBarLines
	}
	// Reserve the header, the help bar and at least one transcript row; add the
	// two border rows when the box is drawn with a border.
	reserved := 3
	if m.borderedInputFits() {
		reserved += 2
	}
	available := m.height - reserved
	if available < 1 {
		return 1
	}
	return min(available, maxInputBarLines)
}

// inputBarHeight is how many rows the prompt box occupies, including its
// border rows when it has any.
func (m *Model) inputBarHeight() int {
	if m.height > 0 && m.height < minHeightForBorderedInput {
		return 1 // the compact single-line form has no border
	}
	return clampInt(m.input.Height(), 1, m.maxInputLines()) + 2
}

// borderedInputFits reports whether the terminal is tall enough for the prompt
// box to keep its border. Below minHeightForBorderedInput the box falls back to
// a single unbordered row, so it cannot push the frame off screen.
func (m *Model) borderedInputFits() bool {
	return m.height <= 0 || m.height >= minHeightForBorderedInput
}
