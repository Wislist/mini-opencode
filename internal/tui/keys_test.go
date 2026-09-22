package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wislist/mini-opencode/internal/config"
)

func keyModel(t *testing.T, state appState) (*Model, *bool) {
	t.Helper()
	cfg := config.Config{User: "u", Assistant: "a"}
	m := New(&cfg, t.TempDir(), "test")
	m.width = 100
	m.height = 40
	m.state = state
	cancelled := false
	m.cancel = func() { cancelled = true }
	return m, &cancelled
}

// TestEscInterruptsRunningAndCompacting is the regression test for moving the
// interrupt key off ctrl+c: esc must cancel the run, and the legacy ctrl+c
// binding must keep working so muscle memory does not accidentally quit.
func TestEscInterruptsRunningAndCompacting(t *testing.T) {
	cases := []struct {
		name  string
		state appState
	}{
		{"running", stateRunning},
		{"compacting", stateCompacting},
	}
	keys := []struct {
		name string
		msg  tea.KeyMsg
	}{
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}},
	}

	for _, tc := range cases {
		for _, key := range keys {
			t.Run(tc.name+"/"+key.name, func(t *testing.T) {
				m, cancelled := keyModel(t, tc.state)
				model, _ := m.Update(key.msg)
				got := model.(*Model)
				if !*cancelled {
					t.Fatal("cancel was not called")
				}
				if got.state != stateIdle {
					t.Fatalf("state = %v, want stateIdle", got.state)
				}
				if !strings.Contains(plainText(strings.Join(got.blocks, "\n")), "interrupted") {
					t.Fatalf("no interrupt notice in transcript: %q", got.blocks)
				}
			})
		}
	}
}

// TestEscWhileIdleDoesNotCancelOrQuit keeps esc from becoming a global
// interrupt: while idle there is nothing to cancel, and esc belongs to the
// command menu and the input field.
func TestEscWhileIdleDoesNotCancelOrQuit(t *testing.T) {
	m, cancelled := keyModel(t, stateIdle)

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := model.(*Model)

	if *cancelled {
		t.Fatal("esc cancelled while idle")
	}
	if got.state != stateIdle {
		t.Fatalf("state = %v, want stateIdle", got.state)
	}
}

// TestCtrlCWhileIdleStillQuits documents that exiting the program did not move
// to esc along with the interrupt binding.
func TestCtrlCWhileIdleStillQuits(t *testing.T) {
	m, cancelled := keyModel(t, stateIdle)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := model.(*Model)

	if *cancelled {
		t.Fatal("ctrl+c cancelled instead of quitting")
	}
	if got.state != stateQuitting {
		t.Fatalf("state = %v, want stateQuitting", got.state)
	}
	if cmd == nil {
		t.Fatal("quit command was not returned")
	}
}
