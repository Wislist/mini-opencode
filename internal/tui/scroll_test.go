package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// newScrollModel builds a model whose viewport contains more lines than it can
// show, so scroll offsets are observable.
func newScrollModel(t *testing.T) *Model {
	t.Helper()
	m := New(nil, "/tmp", "test")
	m.width = 60
	m.height = 20
	m.viewport.Width = 60
	m.viewport.Height = 5
	m.viewport.SetContent(strings.Join([]string{
		"line 1", "line 2", "line 3", "line 4", "line 5",
		"line 6", "line 7", "line 8", "line 9", "line 10",
	}, "\n"))
	return m
}

func TestWheelDownScrollsByTerminalStep(t *testing.T) {
	m := newScrollModel(t)
	m.terminal.wheelLines = 3

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	got := updated.(*Model)
	if got.viewport.YOffset != 3 {
		t.Fatalf("wheel down offset = %d, want 3", got.viewport.YOffset)
	}
}

func TestWheelUpScrollsBackByTerminalStep(t *testing.T) {
	m := newScrollModel(t)
	m.terminal.wheelLines = 3
	m.viewport.SetYOffset(5)

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})
	got := updated.(*Model)
	if got.viewport.YOffset != 2 {
		t.Fatalf("wheel up offset = %d, want 2", got.viewport.YOffset)
	}
}

func TestWheelIgnoresNonPressActionsAndOtherButtons(t *testing.T) {
	m := newScrollModel(t)
	m.terminal.wheelLines = 3
	m.viewport.SetYOffset(4)

	for _, msg := range []tea.MouseMsg{
		{Action: tea.MouseActionMotion, Button: tea.MouseButtonWheelDown},
		{Action: tea.MouseActionRelease, Button: tea.MouseButtonWheelUp},
		{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft},
		{Action: tea.MouseActionPress, Button: tea.MouseButtonRight},
	} {
		updated, _ := m.Update(msg)
		if got := updated.(*Model).viewport.YOffset; got != 4 {
			t.Fatalf("offset changed to %d for %+v", got, msg)
		}
	}
}

func TestArrowKeysScrollOneLine(t *testing.T) {
	m := newScrollModel(t)

	if updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown}); updated.(*Model).viewport.YOffset != 1 {
		t.Fatalf("down offset = %d, want 1", updated.(*Model).viewport.YOffset)
	}
	if updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp}); updated.(*Model).viewport.YOffset != 0 {
		t.Fatalf("up offset = %d, want 0", updated.(*Model).viewport.YOffset)
	}
}

func TestRunningStateScrollsWithWheelAndKeys(t *testing.T) {
	m := newScrollModel(t)
	m.terminal.wheelLines = 2
	m.state = stateRunning

	updated, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if got := updated.(*Model).viewport.YOffset; got != 2 {
		t.Fatalf("running wheel offset = %d, want 2", got)
	}
	if updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown}); updated.(*Model).viewport.YOffset != 3 {
		t.Fatalf("running down offset = %d, want 3", updated.(*Model).viewport.YOffset)
	}
}

func TestDetectTerminalProfilePerProgram(t *testing.T) {
	cases := []struct {
		program   string
		wantLines int
	}{
		{"Apple_Terminal", 1},
		{"iTerm.app", wheelScrollLines},
		{"vscode", wheelScrollLines},
		{"ghostty", wheelScrollLines},
		{"", wheelScrollLines},
	}
	for _, tc := range cases {
		t.Setenv("TERM_PROGRAM", tc.program)
		t.Setenv("TERM", "xterm-256color")
		got := detectTerminalProfile()
		if got.wheelLines != tc.wantLines {
			t.Errorf("TERM_PROGRAM=%q wheelLines = %d, want %d", tc.program, got.wheelLines, tc.wantLines)
		}
		if !got.mouseWheel || !got.alternateScroll {
			t.Errorf("TERM_PROGRAM=%q profile disables wheel handling: %+v", tc.program, got)
		}
	}
}

func TestDetectTerminalProfileFallsBackToTerm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	got := detectTerminalProfile()
	if got.name != "xterm-256color" {
		t.Fatalf("name = %q, want TERM fallback", got.name)
	}
	if got.wheelLines != wheelScrollLines {
		t.Fatalf("wheelLines = %d, want %d", got.wheelLines, wheelScrollLines)
	}
}
