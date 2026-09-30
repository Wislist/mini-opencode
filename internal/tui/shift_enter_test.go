package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Shift+Enter support.
//
// Bubble Tea v1 could not express Shift+Enter at all: its key type had no shift
// field and terminals encode shift+enter and enter as the same CR byte. v2
// negotiates the kitty keyboard protocol (KeyboardEnhancementsMsg) and exposes
// modifiers on the key, which is what makes this possible. These tests pin the
// behaviour that depends on that upgrade.

// TestShiftEnterInsertsNewline is the headline case: Shift+Enter must insert a
// newline rather than send the message.
func TestShiftEnterInsertsNewline(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("first line")

	m = pressKey(m, key("shift+enter"))

	if got := m.input.Value(); got != "first line\n" {
		t.Fatalf("Shift+Enter 后输入为 %q，want %q", got, "first line\n")
	}
}

// TestShiftEnterDoesNotSend 断言 Shift+Enter 不会触发发送 ——
// 这是「换行」与「发送」必须分开的核心要求。
func TestShiftEnterDoesNotSend(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("a question")
	m.state = stateIdle

	m = pressKey(m, key("shift+enter"))

	if m.state == stateRunning {
		t.Fatal("Shift+Enter 触发了发送")
	}
	if !strings.Contains(m.input.Value(), "\n") {
		t.Fatalf("Shift+Enter 没有换行: %q", m.input.Value())
	}
}

// TestEnterStillSendsAfterShiftEnterSupport 断言加了 Shift+Enter 之后
// Enter 仍然是发送 —— 不能以牺牲原有行为为代价。
func TestEnterStillSendsAfterShiftEnterSupport(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("hello")

	m = pressKey(m, key("enter"))

	// Enter 必须走发送路径：输入被清空（或进入运行态），而不是留下换行。
	if strings.Contains(m.input.Value(), "\n") {
		t.Fatalf("Enter 插入了换行: %q", m.input.Value())
	}
}

// TestNewlineKeysAllWork 断言三个换行键都生效：
// Shift+Enter 是首选，Ctrl+J 与 Alt+Enter 是不支持该协议终端上的回退。
func TestNewlineKeysAllWork(t *testing.T) {
	for _, name := range []string{"shift+enter", "ctrl+j", "alt+enter"} {
		t.Run(name, func(t *testing.T) {
			m := inputTestModel(t)
			m.input.SetValue("x")
			m = pressKey(m, key(name))
			if !strings.Contains(m.input.Value(), "\n") {
				t.Fatalf("%s 没有插入换行: %q", name, m.input.Value())
			}
		})
	}
}

// TestShiftEnterRepeatedInsertsMultipleNewlines 覆盖连续换行。
func TestShiftEnterRepeatedInsertsMultipleNewlines(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("x")

	m = pressKey(m, key("shift+enter"))
	m = pressKey(m, key("shift+enter"))

	if got := m.input.Value(); got != "x\n\n" {
		t.Fatalf("连续 Shift+Enter 得到 %q，want %q", got, "x\n\n")
	}
}

// TestShiftEnterWorksWhileRunning 断言运行中也能在输入框里换行。
func TestShiftEnterWorksWhileRunning(t *testing.T) {
	m := inputTestModel(t)
	m.state = stateRunning

	m = pressKey(m, key("shift+enter"))

	if !strings.Contains(m.input.Value(), "\n") {
		t.Fatalf("运行中 Shift+Enter 没有换行: %q", m.input.Value())
	}
}

// TestKeyboardEnhancementsMessageIsHandled 断言终端能力消息不会让程序崩溃，
// 并且程序能记住终端是否支持按键消歧。
func TestKeyboardEnhancementsMessageIsHandled(t *testing.T) {
	m := inputTestModel(t)

	// 终端报告支持消歧（flags > 0）
	updated, _ := m.Update(tea.KeyboardEnhancementsMsg{Flags: 1})
	m = updated.(*Model)
	if !m.supportsShiftEnter {
		t.Fatal("没有记录终端支持按键消歧")
	}

	// 终端不支持时也不该 panic
	m2 := inputTestModel(t)
	updated2, _ := m2.Update(tea.KeyboardEnhancementsMsg{Flags: 0})
	m2 = updated2.(*Model)
	if m2.supportsShiftEnter {
		t.Fatal("不支持时错误地标记为支持")
	}
}

// TestHelpBarMentionsNewlineKey 断言帮助栏提示了换行键，
// 否则用户不会知道可以多行输入。
func TestHelpBarMentionsNewlineKey(t *testing.T) {
	m := inputTestModel(t)
	bar := plainText(m.renderHelpBar())
	if !strings.Contains(bar, "shift+enter") && !strings.Contains(bar, "ctrl+j") {
		t.Fatalf("帮助栏没有提示换行键: %q", bar)
	}
}
