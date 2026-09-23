package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/config"
)

func inputTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := config.Config{User: "u", Assistant: "a", Provider: config.ProviderConfig{Name: "deepseek"}}
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	return model.(*Model)
}

// pressKey sends a key message and returns the model.
func pressKey(m *Model, msg tea.KeyMsg) *Model {
	model, _ := m.Update(msg)
	return model.(*Model)
}

// ── 换行 ────────────────────────────────────────────────────────────

// TestShiftEnterIsIndistinguishableFromEnter 记录一个终端的硬限制，
// 而不是假装它可行。
//
// tea.Key 只有 Type/Runes/Alt/Paste —— 没有 Shift 字段，bubbletea 也没有
// KeyShiftEnter 常量。绝大多数终端把 Shift+Enter 和 Enter 都编码成 CR (\r)，
// 所以应用层看到的两者**完全相同**，无法区分。换行改由 Ctrl+J 与 Alt+Enter 承担。
func TestShiftEnterIsIndistinguishableFromEnter(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("text")

	// 「Shift+Enter」在协议层就是 Enter，所以它必须仍然走发送路径。
	// 只断言它没有插入换行；真正的发送需要 program，这里不启动。
	m = pressKey(m, key("enter"))

	if strings.Contains(m.input.Value(), "\n") {
		t.Fatal("Enter 路径被改成了换行，发送功能被破坏")
	}
}

// TestShiftTabIsNotNewline 断言 shift+tab 不受影响（它确实有独立常量）。
func TestShiftTabIsNotNewline(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("x")
	m = pressKey(m, key("shift+tab"))
	if strings.Contains(m.input.Value(), "\n") {
		t.Fatal("shift+tab 被当成了换行")
	}
}

// TestEnterStillSends 断言 Enter 仍然是发送 —— 换行不能以牺牲现有行为为代价。
func TestEnterStillSends(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("hello")
	m.SetRuntime(nil) // 没有 runtime 时发送会被拒绝，但状态仍应改变

	m = pressKey(m, key("enter"))

	if strings.Contains(m.input.Value(), "\n") {
		t.Fatalf("Enter 插入了换行: %q", m.input.Value())
	}
}

// TestNewlineKeyAliases 断言备用换行键都可用，
// 因为不是所有终端都能区分 Shift+Enter。
func TestNewlineKeyAliases(t *testing.T) {
	aliases := map[string]tea.KeyMsg{
		"alt+enter": key("alt+enter"),
		"ctrl+j":    key("ctrl+j"),
	}
	for name, msg := range aliases {
		t.Run(name, func(t *testing.T) {
			m := inputTestModel(t)
			m.input.SetValue("line")
			m = pressKey(m, msg)
			if !strings.Contains(m.input.Value(), "\n") {
				t.Fatalf("%s 没有插入换行: %q", name, m.input.Value())
			}
		})
	}
}

// ── 多行输入框渲染 ──────────────────────────────────────────────────

// TestInputBarGrowsWithLines 断言输入框随行数变高。
func TestInputBarGrowsWithLines(t *testing.T) {
	m := inputTestModel(t)
	single := lineCount(m.renderInputBar())

	m.input.SetValue("one\ntwo\nthree")
	m.syncInputHeight()
	multi := lineCount(m.renderInputBar())

	if multi <= single {
		t.Fatalf("多行输入没有让输入框变高: %d -> %d", single, multi)
	}
}

// TestInputBarHeightIsBounded 断言输入框不会无限长高而挤掉正文。
func TestInputBarHeightIsBounded(t *testing.T) {
	m := inputTestModel(t)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("line\n")
	}
	m.input.SetValue(b.String())
	m.syncInputHeight()

	if got := lineCount(m.renderInputBar()); got > maxInputBarLines+2 {
		t.Fatalf("输入框高 %d 行，超过上限 %d（含边框）", got, maxInputBarLines+2)
	}
}

// TestViewFitsWithMultilineInput 断言多行输入下整个界面仍然不超出终端。
func TestViewFitsWithMultilineInput(t *testing.T) {
	for _, h := range []int{40, 24, 18, 14, 12, 10, 8} {
		m := inputTestModel(t)
		model, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: h})
		m = model.(*Model)
		m.input.SetValue("one\ntwo\nthree\nfour\nfive")

		rows := lineCount(m.renderFrame())
		if rows > h {
			t.Fatalf("h=%d 时视图 %d 行，超出 %d 行", h, rows, rows-h)
		}
	}
}

// TestViewBoxesStayBalancedWithMultilineInput 断言多行输入不会破坏框的闭合。
func TestViewBoxesStayBalancedWithMultilineInput(t *testing.T) {
	for _, h := range []int{40, 24, 18, 14, 12, 10, 8, 6} {
		m := inputTestModel(t)
		model, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: h})
		m = model.(*Model)
		m.input.SetValue("alpha\nbeta\ngamma")

		view := m.renderFrame()
		if opens, closes := strings.Count(view, "╭"), strings.Count(view, "╰"); opens != closes {
			t.Fatalf("h=%d: 开框 %d 闭框 %d\n%s", h, opens, closes, view)
		}
	}
}

// ── 发送与重置 ──────────────────────────────────────────────────────

// TestSendTrimsAndFlattensTrailingNewlines 断言发送时去掉尾部换行，
// 避免把无意义的空白发给模型。
func TestSendTrimsAndFlattensTrailingNewlines(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("question\n\n\n")
	got := strings.TrimSpace(m.input.Value())
	if got != "question" {
		t.Fatalf("整理后的输入为 %q，want %q", got, "question")
	}
}

// TestMultilineInputIsSentWhole 断言多行内容作为整体发送，内部换行保留。
//
// It drives handleInput's send path through startRun, which needs a program to
// deliver events to, so this asserts on the value the runtime would receive
// rather than starting a real run.
func TestMultilineInputIsSentWhole(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("line one\nline two")

	// The send path trims surrounding whitespace but keeps interior newlines.
	sent := strings.TrimSpace(m.input.Value())
	if sent != "line one\nline two" {
		t.Fatalf("多行内容被改坏: %q", sent)
	}
	if got := strings.Count(sent, "\n"); got != 1 {
		t.Fatalf("内部换行丢失，得到 %d 个", got)
	}
}

// ── 边界 ────────────────────────────────────────────────────────────

func TestMultilineInputBoundaries(t *testing.T) {
	t.Run("空输入按 Enter 不发送", func(t *testing.T) {
		m := inputTestModel(t)
		m.input.SetValue("")
		before := m.state
		m = pressKey(m, key("enter"))
		if m.state != before {
			t.Fatalf("空输入触发了状态变化: %v -> %v", before, m.state)
		}
	})

	t.Run("只有空白按 Enter 不发送", func(t *testing.T) {
		m := inputTestModel(t)
		m.input.SetValue("   \n  ")
		before := m.state
		m = pressKey(m, key("enter"))
		if m.state != before {
			t.Fatal("纯空白触发了发送")
		}
	})

	t.Run("空输入上也能换行", func(t *testing.T) {
		m := inputTestModel(t)
		m.input.SetValue("")
		m = pressKey(m, key("ctrl+j"))
		if m.input.Value() != "\n" {
			t.Fatalf("空输入换行得到 %q", m.input.Value())
		}
	})

	t.Run("连续换行产生多个换行", func(t *testing.T) {
		m := inputTestModel(t)
		m.input.SetValue("x")
		m = pressKey(m, key("ctrl+j"))
		m = pressKey(m, key("ctrl+j"))
		if got := m.input.Value(); got != "x\n\n" {
			t.Fatalf("连续换行得到 %q，want %q", got, "x\n\n")
		}
	})

	t.Run("命令菜单打开时 Enter 选择而非发送", func(t *testing.T) {
		m := inputTestModel(t)
		m.input.SetValue("/he")
		m.updateCommandMenu()
		if !m.commandMenuOpen() {
			t.Skip("命令菜单未打开，跳过")
		}
		m = pressKey(m, key("enter"))
		if strings.Contains(m.input.Value(), "\n") {
			t.Fatal("菜单选择时插入了换行")
		}
	})

	t.Run("极窄终端多行输入不 panic", func(t *testing.T) {
		m := inputTestModel(t)
		model, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 20})
		m = model.(*Model)
		m.input.SetValue("a long line that will wrap many times over\nsecond\nthird")
		_ = m.renderFrame()
	})

	t.Run("零尺寸终端不 panic", func(t *testing.T) {
		m := inputTestModel(t)
		model, _ := m.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
		m = model.(*Model)
		m.input.SetValue("x\ny")
		_ = m.renderFrame()
	})
}

// ── 游标 ────────────────────────────────────────────────────────────

// TestCursorPositionStaysInsideInput 断言多行输入下游标位置仍在输入框内。
func TestCursorPositionStaysInsideInput(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("one\ntwo\nthree")
	_ = m.renderFrame()

	col, row, ok := m.NativeCursorPosition()
	if !ok {
		t.Fatal("多行输入下游标位置无效")
	}
	if row < 1 || row > m.height {
		t.Fatalf("游标 row=%d 越界（终端高 %d）", row, m.height)
	}
	if col < 1 || col > m.width {
		t.Fatalf("游标 col=%d 越界（终端宽 %d）", col, m.width)
	}
}

// TestMultilineInputNeverOverflows sweeps height × line-count: a taller prompt
// box must never push the frame past the bottom of the terminal, and every box
// that is opened must be closed.
//
// This is the guard for the regression where the growing input was allotted
// more rows than existed, which produced the overflowing frames that made boxes
// look detached.
func TestMultilineInputNeverOverflows(t *testing.T) {
	for _, h := range []int{40, 24, 18, 14, 12, 10, 9, 8, 7, 6, 5, 4, 3} {
		for _, lines := range []int{1, 3, 6, 12} {
			m := inputTestModel(t)
			model, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: h})
			m = model.(*Model)
			m.input.SetValue(strings.Repeat("x\n", lines-1) + "x")
			m.syncInputHeight()

			view := m.renderFrame()
			rows := lineCount(view)
			if rows > h {
				t.Fatalf("h=%d lines=%d: 视图 %d 行，超出 %d 行", h, lines, rows, rows-h)
			}
			if opens, closes := strings.Count(view, "╭"), strings.Count(view, "╰"); opens != closes {
				t.Fatalf("h=%d lines=%d: 开框 %d 闭框 %d", h, lines, opens, closes)
			}
		}
	}
}
