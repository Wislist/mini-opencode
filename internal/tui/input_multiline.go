package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

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
	m.input.SetHeight(clampInt(m.inputLineCount(), 1, m.maxInputLines()))
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

// inputLineCount is the number of visual lines the current value occupies,
// taking wrapping into account so a long single line also grows the box.
func (m *Model) inputLineCount() int {
	value := m.input.Value()
	if value == "" {
		return 1
	}
	width := m.input.Width()
	if width <= 0 {
		// Width is not known yet (before the first resize); count explicit
		// newlines only rather than guessing a wrap width.
		return strings.Count(value, "\n") + 1
	}
	total := 0
	for _, line := range strings.Split(value, "\n") {
		w := lipgloss.Width(line)
		if w <= 0 {
			total++
			continue
		}
		// A line occupying exactly `width` cells wraps to the next row once the
		// caret sits at the end, which is why the ceiling division is used.
		total += (w + width - 1) / width
	}
	return max(1, total)
}

// inputBarHeight is how many rows the prompt box occupies, including its
// border rows when it has any.
func (m *Model) inputBarHeight() int {
	if m.height > 0 && m.height < minHeightForBorderedInput {
		return 1 // the compact single-line form has no border
	}
	return clampInt(m.inputLineCount(), 1, m.maxInputLines()) + 2
}

// borderedInputFits reports whether the terminal is tall enough for the prompt
// box to keep its border. Below minHeightForBorderedInput the box falls back to
// a single unbordered row, so it cannot push the frame off screen.
func (m *Model) borderedInputFits() bool {
	return m.height <= 0 || m.height >= minHeightForBorderedInput
}
