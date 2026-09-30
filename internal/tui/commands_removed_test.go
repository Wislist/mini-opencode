package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/config"
)

func freshIdleModel(t *testing.T) *Model {
	t.Helper()
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return model.(*Model)
}

// 删掉的命令在 TUI 里也必须是 unknown，而不是落进对话当提示词发出去。
func TestTUIRemovedCommandsAreUnknown(t *testing.T) {
	for _, cmd := range []string{"/help", "/version", "/tools", "/plan", "/workspace", "/key", "/name", "/key sk-abc", "/name bob"} {
		t.Run(cmd, func(t *testing.T) {
			m := freshIdleModel(t)
			before := len(m.blocks)

			m.handleInput(cmd)

			if m.state != stateIdle {
				t.Fatalf("state = %v, want idle", m.state)
			}
			if len(m.blocks) <= before {
				t.Fatal("nothing was reported")
			}
			joined := strings.Join(m.blocks, "\n")
			if !strings.Contains(joined, "unknown command") {
				t.Fatalf("output %q does not reject the command", joined)
			}
			// The remaining plan-mode switch lives on tab; a deleted command
			// must not touch it.
			if m.mode == ModePlan {
				t.Fatal("a removed command turned plan mode on")
			}
		})
	}
}

func TestCommandMenuHidesRemovedCommands(t *testing.T) {
	names := map[string]bool{}
	for _, item := range filterCommands("") {
		names[item.Name] = true
	}
	for _, cmd := range []string{"/help", "/version", "/tools", "/plan", "/workspace", "/key", "/name"} {
		if names[cmd] {
			t.Fatalf("command menu still offers %s", cmd)
		}
	}
	for _, want := range []string{"/status", "/provider", "/model", "/permissions", "/compact", "/session", "/quit"} {
		if !names[want] {
			t.Fatalf("command menu lost %s", want)
		}
	}
}

// plan 模式只剩 tab 一个入口，它必须还能用。
func TestPlanModeStillReachableByTab(t *testing.T) {
	m := freshIdleModel(t)

	m = pressKey(m, key("tab"))
	if m.mode != ModePlan {
		t.Fatalf("tab did not enter plan mode: %v", m.mode)
	}
	m = pressKey(m, key("tab"))
	if m.mode != ModeCode {
		t.Fatalf("tab did not leave plan mode: %v", m.mode)
	}
}

// 界面上的固定文案（占位符、欢迎语、帮助栏）不能继续推荐已经删掉的命令。
func TestChromeNoLongerAdvertisesRemovedCommands(t *testing.T) {
	m := freshIdleModel(t)
	m.Init()

	surfaces := map[string]string{
		"input placeholder": m.input.Placeholder,
		"welcome block":     strings.Join(m.blocks, "\n"),
		"help bar":          plainText(m.renderHelpBar()),
	}
	for name, text := range surfaces {
		for _, cmd := range []string{"/help", "/version", "/tools", "/plan", "/workspace", "/key", "/name"} {
			if strings.Contains(text, cmd) {
				t.Fatalf("%s still advertises %s: %q", name, cmd, text)
			}
		}
	}
}

// 窄终端下先丢命令列表、再丢快捷键，最后什么都不留——但不能留下已删命令的名字。
func TestNarrowHelpBarDropsRemovedHints(t *testing.T) {
	m := freshIdleModel(t)
	for _, width := range []int{200, 80, 40, 20, 12, 6} {
		model, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m = model.(*Model)
		bar := plainText(m.renderHelpBar())
		if strings.Contains(bar, "/help") || strings.Contains(bar, "/tools") {
			t.Fatalf("width %d: help bar still advertises removed commands: %q", width, bar)
		}
		if strings.Contains(bar, "tab mode") == false && width >= 100 {
			t.Fatalf("width %d: key hints vanished: %q", width, bar)
		}
	}
}
