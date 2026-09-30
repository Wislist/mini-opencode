package tui

// Menu layout helpers shared by every bottom overlay: /permissions, /provider,
// /model, the add form and the fetched-model multi-select.
//
// Two rules live here because they are easy to get subtly wrong once per menu:
// a row budget measured in *rendered* rows (a narrow terminal folds one entry
// into several), and a window that follows the cursor so a list longer than the
// screen stays navigable.

// menuNonItemRows is the part of the frame that is never list content: the app
// header, the viewport's minimum row, the overlay's borders, and the input bar.
// The subtraction is deliberately not "height minus one" — the overlays sit
// above the input, so the input always gets its share first.
const menuNonItemRows = 6

// menuItemBudget is how many list rows fit on screen. Zero means the height is
// unknown (a model built before the first WindowSizeMsg), which callers treat as
// "show everything".
func (m *Model) menuItemBudget() int {
	if m.height <= 0 {
		return 0
	}
	return max(1, m.height-menuNonItemRows)
}

// visibleMenuWindow returns the [start, end) slice of a list to draw so the
// cursor is always inside it. budget <= 0 means no limit.
func visibleMenuWindow(total, cursor, budget int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if budget <= 0 || budget >= total {
		return 0, total
	}
	cursor = min(max(cursor, 0), total-1)
	start := cursor - budget/2
	if start < 0 {
		start = 0
	}
	if start+budget > total {
		start = total - budget
	}
	return start, start + budget
}

// windowLabel describes a windowed list for the overlay header, or "" when
// everything is visible. Without it a scrolled list looks like the whole list.
func windowLabel(start, end, total int) string {
	if start == 0 && end >= total {
		return ""
	}
	return " · 显示 " + itoa(start+1) + "-" + itoa(end) + "/" + itoa(total)
}

// fitMenuRows trims optional rows so an overlay fits the terminal, keeping the
// header plus the first `essential` rows no matter how short the window is.
//
// Rows are counted as already-wrapped physical rows by the callers, because a
// narrow terminal folds one logical entry into several and trimming by entry
// would still overflow.
func (m *Model) fitMenuRows(lines []string, essential int) []string {
	if m.height <= 0 {
		return lines
	}
	// Header (1) + borders (2) + at least one transcript row.
	const nonMenuRows = 4
	available := m.height - nonMenuRows
	if available >= len(lines) {
		return lines
	}
	essential = max(1, min(essential, len(lines)))
	if available <= essential {
		return lines[:essential]
	}
	return lines[:available]
}
