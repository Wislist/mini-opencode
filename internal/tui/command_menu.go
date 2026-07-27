package tui

import (
	"sort"
	"strings"
)

// CommandItem pairs a slash command with its English description.
type CommandItem struct {
	Name string
	Desc string
}

// commandList is the full set of slash commands shown in the autocomplete
// menu, in display order.
var commandList = []CommandItem{
	{"/help", "show this help message"},
	{"/version", "show version information"},
	{"/tools", "list registered tools"},
	{"/workspace", "show workspace root and allowed paths"},
	{"/status", "show git status and context usage"},
	{"/session", "list and switch to a saved conversation"},
	{"/newsession", "start a new conversation"},
	{"/compact", "summarize and replace the conversation context"},
	{"/key", "set the API key for the current provider"},
	{"/name", "set or show the user and assistant display names"},
	{"/quit", "exit the application"},
}

// filterCommands returns the commands whose name starts with prefix,
// preserving display order. An empty prefix returns all commands.
func filterCommands(prefix string) []CommandItem {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return commandList
	}
	var matched []CommandItem
	for _, c := range commandList {
		if strings.HasPrefix(c.Name, prefix) {
			matched = append(matched, c)
		}
	}
	return matched
}

// commandMenuOpen reports whether the command autocomplete menu should be
// visible for the current input value.
func (m *Model) commandMenuOpen() bool {
	return m.state == stateIdle && strings.HasPrefix(m.input.Value(), "/")
}

// updateCommandMenu refreshes the filtered command list and keeps the cursor
// in range. Called after every keystroke in idle state.
func (m *Model) updateCommandMenu() {
	if !m.commandMenuOpen() {
		m.commandFiltered = nil
		m.commandCursor = 0
		return
	}
	m.commandFiltered = filterCommands(m.input.Value())
	if m.commandCursor >= len(m.commandFiltered) {
		m.commandCursor = max(0, len(m.commandFiltered)-1)
	}
}

// commandMenuMove changes the cursor by delta, wrapping at the edges.
func (m *Model) commandMenuMove(delta int) {
	n := len(m.commandFiltered)
	if n == 0 {
		return
	}
	m.commandCursor = (m.commandCursor + delta) % n
	if m.commandCursor < 0 {
		m.commandCursor += n
	}
}

// commandMenuSelect returns the currently highlighted command, or "" if the
// menu is empty.
func (m *Model) commandMenuSelect() string {
	if m.commandCursor < 0 || m.commandCursor >= len(m.commandFiltered) {
		return ""
	}
	return m.commandFiltered[m.commandCursor].Name
}

// sortedCommandList returns commandList sorted by name (used by tests).
func sortedCommandList() []CommandItem {
	out := make([]CommandItem, len(commandList))
	copy(out, commandList)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
