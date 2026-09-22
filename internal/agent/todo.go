package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Todo statuses. Exactly one entry may be in progress while work is ongoing.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoCompleted  = "completed"
)

// TodoItem is one checklist entry of the session task list.
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// ParseTodos decodes a stored todo list, returning nil for empty or invalid
// payloads.
func ParseTodos(raw string) []TodoItem {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var items []TodoItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	return items
}

// NormalizeTodoStatus maps the status spellings models actually emit onto the
// three canonical values.
func NormalizeTodoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case TodoInProgress, "in-progress", "doing":
		return TodoInProgress
	case TodoCompleted, "done", "complete":
		return TodoCompleted
	default:
		return TodoPending
	}
}

// RenderTodos renders a todo list as a compact checklist. Completed items stay
// visible so progress is explicit.
func RenderTodos(items []TodoItem) string {
	if len(items) == 0 {
		return "todo list cleared"
	}
	var b strings.Builder
	done := 0
	for _, item := range items {
		switch NormalizeTodoStatus(item.Status) {
		case TodoCompleted:
			done++
			fmt.Fprintf(&b, "[x] %s\n", item.Content)
		case TodoInProgress:
			fmt.Fprintf(&b, "[>] %s\n", item.Content)
		default:
			fmt.Fprintf(&b, "[ ] %s\n", item.Content)
		}
	}
	fmt.Fprintf(&b, "(%d/%d done)", done, len(items))
	return strings.TrimRight(b.String(), "\n")
}

// TodoProgress summarizes a todo list for the run loop: what is left, what is
// in progress, and the next actionable entry.
type TodoProgress struct {
	// Items is the full list this summary was derived from, so callers can
	// render it without re-reading the store.
	Items      []TodoItem
	Total      int
	Completed  int
	InProgress string
	Next       string
	// Fingerprint identifies this exact list and status set, so the run loop
	// can tell whether a continuation round moved anything forward.
	Fingerprint string
}

// Outstanding reports whether work remains in the list.
func (p TodoProgress) Outstanding() bool {
	return p.Total > 0 && p.Completed < p.Total
}

// SummarizeTodos derives the run-loop view of a todo list.
func SummarizeTodos(items []TodoItem) TodoProgress {
	progress := TodoProgress{Items: items, Total: len(items)}
	if len(items) == 0 {
		return progress
	}
	var fingerprint strings.Builder
	for i, item := range items {
		status := NormalizeTodoStatus(item.Status)
		fmt.Fprintf(&fingerprint, "%d:%s:%s\n", i, status, item.Content)
		switch status {
		case TodoCompleted:
			progress.Completed++
		case TodoInProgress:
			if progress.InProgress == "" {
				progress.InProgress = item.Content
			}
		default:
			if progress.Next == "" {
				progress.Next = item.Content
			}
		}
	}
	// An in-progress entry with a still-pending tail is the actionable one.
	if progress.Next == "" && progress.InProgress != "" && progress.Completed < progress.Total {
		progress.Next = progress.InProgress
	}
	progress.Fingerprint = fingerprint.String()
	return progress
}
