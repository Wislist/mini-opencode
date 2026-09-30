package tui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// key builds a Bubble Tea v2 key press from a keystroke name.
//
// v2 replaced the typed key constants with a KeyMsg interface whose value is
// matched by its String() form (for example "enter", "ctrl+j", "shift+enter"),
// so tests name the key the same way the handler matches it. That keeps a test
// honest: if the handler's string stops matching, the test fails rather than
// silently exercising a different branch.
func key(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: uv.ModShift}
	case "up":
		return tea.KeyPressMsg{Code: uv.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: uv.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: uv.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: uv.KeyRight}
	case "pgup":
		return tea.KeyPressMsg{Code: uv.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: uv.KeyPgDown}
	case "shift+enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: uv.ModShift}
	case "alt+enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: uv.ModAlt}
	case "ctrl+j":
		// Ctrl+J is LF, which v2 models as the ctrl modifier on 'j'.
		return tea.KeyPressMsg{Code: 'j', Mod: uv.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: uv.ModCtrl}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: uv.ModCtrl}
	case "ctrl+t":
		return tea.KeyPressMsg{Code: 't', Mod: uv.ModCtrl}
	}
	// A single rune key, e.g. "y" or "/".
	runes := []rune(name)
	if len(runes) == 1 {
		return tea.KeyPressMsg{Code: runes[0], Text: name}
	}
	panic("keytest: unknown key name " + name)
}

// Mouse helpers mirroring the key helper: v2 splits mouse input into distinct
// message types, so tests build the concrete type they mean.
func mouseClick(x, y int) tea.MouseMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func mouseMotion(x, y int) tea.MouseMsg {
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func mouseRelease(x, y int) tea.MouseMsg {
	return tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func wheelUp() tea.MouseMsg   { return tea.MouseWheelMsg{Button: tea.MouseWheelUp} }
func wheelDown() tea.MouseMsg { return tea.MouseWheelMsg{Button: tea.MouseWheelDown} }

// typeText feeds a string to the model one rune at a time, the way a terminal
// delivers typing. Named keys (enter, tab, esc) stay available through key().
func typeText(m *Model, text string) *Model {
	for _, r := range text {
		model, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = model.(*Model)
	}
	return m
}
