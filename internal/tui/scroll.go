package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Scroll behavior is tuned per terminal. macOS terminals disagree on how they
// deliver wheel input: some translate the wheel into cursor keys (xterm
// alternate-scroll mode, ESC[?1007h), others report real mouse events once
// tracking is on. Terminal.app additionally accelerates wheel reporting, so a
// single physical notch can arrive as a burst of events.
const (
	// wheelScrollLines is the number of transcript lines moved per wheel event.
	wheelScrollLines = 3
	// keyScrollLines is the number of transcript lines moved per arrow key.
	keyScrollLines = 1
)

// terminalProfile describes how the host terminal delivers scroll input.
type terminalProfile struct {
	// name is the detected terminal program, for diagnostics.
	name string
	// mouseWheel is true when the terminal reliably reports wheel events as
	// mouse input while alternate-scroll mode keeps cursor keys for the
	// keyboard.
	mouseWheel bool
	// alternateScroll is true when ESC[?1007h should stay enabled so the wheel
	// also produces cursor-key fallbacks on terminals that suppress mouse
	// events while text selection is active.
	alternateScroll bool
	// wheelLines overrides wheelScrollLines when the terminal sends bursts.
	wheelLines int
}

// detectTerminalProfile inspects the environment to pick scroll behavior.
// Terminals that are known to report wheel events with mouse tracking enabled
// get exact wheel handling; everything else keeps the alternate-scroll
// translation and scrolls a larger step per cursor key.
func detectTerminalProfile() terminalProfile {
	program := os.Getenv("TERM_PROGRAM")
	term := os.Getenv("TERM")

	profile := terminalProfile{
		name:            program,
		mouseWheel:      true,
		alternateScroll: true,
		wheelLines:      wheelScrollLines,
	}
	if profile.name == "" {
		profile.name = term
	}

	switch strings.ToLower(program) {
	case "apple_terminal":
		// Terminal.app batches wheel motion into repeated key/mouse events, so
		// a smaller step keeps the transcript from jumping.
		profile.wheelLines = 1
	case "iterm.app", "ghostty", "wezterm", "kitty", "alacritty":
		profile.wheelLines = wheelScrollLines
	case "vscode":
		profile.wheelLines = wheelScrollLines
	default:
		if strings.Contains(term, "256color") || strings.Contains(term, "xterm") {
			profile.wheelLines = wheelScrollLines
		}
	}

	return profile
}

// scrollLines moves the transcript by n lines. Positive scrolls down.
func (m *Model) scrollLines(n int) {
	if n == 0 {
		return
	}
	if n > 0 {
		m.viewport.LineDown(n)
		return
	}
	m.viewport.LineUp(-n)
}

// handleMouse scrolls the transcript for wheel events and ignores every other
// mouse action so text selection and clicks keep their default terminal
// behavior.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollLines(-m.terminal.wheelLines)
	case tea.MouseButtonWheelDown:
		m.scrollLines(m.terminal.wheelLines)
	}
	return m, nil
}
