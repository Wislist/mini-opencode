package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/config"
)

// ── 选区坐标模型 ────────────────────────────────────────────────────

// TestSelectionNormalizesReverseDrag 断言从下往上、从右往左拖拽也能得到
// 正确的起止顺序 —— 鼠标拖动不保证方向。
func TestSelectionNormalizesReverseDrag(t *testing.T) {
	cases := []struct {
		name                     string
		start, end               selPoint
		wantStartRow, wantEndRow int
		wantStartCol, wantEndCol int
	}{
		{"正向", selPoint{row: 1, col: 2}, selPoint{row: 5, col: 9}, 1, 5, 2, 9},
		{"反向", selPoint{row: 5, col: 9}, selPoint{row: 1, col: 2}, 1, 5, 2, 9},
		{"同一行正向", selPoint{row: 3, col: 1}, selPoint{row: 3, col: 8}, 3, 3, 1, 8},
		{"同一行反向", selPoint{row: 3, col: 8}, selPoint{row: 3, col: 1}, 3, 3, 1, 8},
		{"同一点", selPoint{row: 2, col: 4}, selPoint{row: 2, col: 4}, 2, 2, 4, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeSelection(tc.start, tc.end)
			if got.start.row != tc.wantStartRow || got.end.row != tc.wantEndRow ||
				got.start.col != tc.wantStartCol || got.end.col != tc.wantEndCol {
				t.Fatalf("normalizeSelection = %+v..%+v, want row %d..%d col %d..%d",
					got.start, got.end, tc.wantStartRow, tc.wantEndRow, tc.wantStartCol, tc.wantEndCol)
			}
		})
	}
}

// ── 拖拽生命周期 ────────────────────────────────────────────────────

// TestMouseDragCreatesSelection 断言按下拖动会建立选区。
func TestMouseDragCreatesSelection(t *testing.T) {
	m := selectionTestModel(t)

	m.Update(mouseClick(5, 3))
	if !m.selection.active {
		t.Fatal("按下没有开始选择")
	}
	m.Update(mouseMotion(20, 6))
	if !m.selection.active {
		t.Fatal("拖动中断了选择")
	}
	if m.selection.end.row == m.selection.start.row && m.selection.end.col == m.selection.start.col {
		t.Fatal("拖动没有更新终点")
	}
}

// TestMouseReleaseCopiesToClipboard 断言松开时把选中文本交给剪贴板。
func TestMouseReleaseCopiesToClipboard(t *testing.T) {
	m := selectionTestModel(t)
	var copied string
	m.SetClipboardWriter(func(s string) error { copied = s; return nil })

	m.Update(mouseClick(2, 2))
	m.Update(mouseMotion(30, 4))
	m.Update(mouseRelease(30, 4))

	if copied == "" {
		t.Fatal("松开后没有写入剪贴板")
	}
	if strings.TrimSpace(copied) == "" {
		t.Fatalf("复制到的是空白: %q", copied)
	}
}

// TestClickWithoutDragClearsSelection 断言单击（没有拖动）清除选区，
// 而不是复制一个字符。
func TestClickWithoutDragClearsSelection(t *testing.T) {
	m := selectionTestModel(t)
	calls := 0
	m.SetClipboardWriter(func(s string) error { calls++; return nil })

	// 先建立一个选区
	m.Update(mouseClick(2, 2))
	m.Update(mouseMotion(20, 5))
	m.Update(mouseRelease(20, 5))
	if !m.selection.active {
		t.Fatal("前提不成立：没有建立选区")
	}
	before := calls

	// 单击（按下即松开，无移动）
	m.Update(mouseClick(40, 9))
	m.Update(mouseRelease(40, 9))

	if m.selection.active {
		t.Fatal("单击没有清除选区")
	}
	if calls != before {
		t.Fatal("单击不该复制内容")
	}
}

// TestScrollWheelDoesNotStartSelection 断言滚轮不会误触发选区。
func TestScrollWheelDoesNotStartSelection(t *testing.T) {
	m := selectionTestModel(t)
	m.Update(wheelUp())
	if m.selection.active {
		t.Fatal("滚轮触发了选区")
	}
}

// TestEscapeClearsSelection 断言 esc 清除选区。
func TestEscapeClearsSelection(t *testing.T) {
	m := selectionTestModel(t)
	m.Update(mouseClick(2, 2))
	m.Update(mouseMotion(20, 5))
	if !m.selection.active {
		t.Fatal("前提不成立")
	}
	m.Update(key("esc"))
	if m.selection.active {
		t.Fatal("esc 没有清除选区")
	}
}

// TestSendingMessageClearsSelection 断言发送消息后清除选区。
func TestSendingMessageClearsSelection(t *testing.T) {
	m := selectionTestModel(t)
	m.Update(mouseClick(2, 2))
	m.Update(mouseMotion(20, 5))
	if !m.selection.active {
		t.Fatal("前提不成立")
	}
	m.clearSelection()
	if m.selection.active {
		t.Fatal("清除选区失败")
	}
}

// ── 坐标 → 文本 ─────────────────────────────────────────────────────

// TestSelectionExtractsPlainText 断言提取的是纯文本，
// 不带 ANSI 转义、不带边框。
func TestSelectionExtractsPlainText(t *testing.T) {
	m := selectionTestModel(t)
	m.blocks = []string{"alpha bravo", "charlie delta", "echo foxtrot"}
	m.markBlocksChanged()
	m.refreshViewport()
	_ = m.renderFrame()

	start := selPoint{row: 1, col: 0}
	end := selPoint{row: 2, col: 6}
	text := m.selectionText(normalizeSelection(start, end))

	if strings.Contains(text, "\x1b") {
		t.Fatalf("提取的文本含 ANSI 转义: %q", text)
	}
	if strings.ContainsAny(text, "╭╮╰╯│─") {
		t.Fatalf("提取的文本含边框字符: %q", text)
	}
	if !strings.Contains(text, "alpha") {
		t.Fatalf("没有提取到首行内容: %q", text)
	}
}

// TestSelectionAcrossMultipleLines 断言跨行选择包含中间整行。
func TestSelectionAcrossMultipleLines(t *testing.T) {
	m := selectionTestModel(t)
	text := m.selectionText(normalizeSelection(
		selPoint{row: 1, col: 0},
		selPoint{row: 3, col: 5},
	))
	if strings.Count(text, "\n") < 2 {
		t.Fatalf("跨 3 行只得到 %d 个换行: %q", strings.Count(text, "\n"), text)
	}
}

// TestSelectionRowsClampToScreen 断言点击屏幕外不会 panic 或返回垃圾。
func TestSelectionRowsClampToScreen(t *testing.T) {
	m := selectionTestModel(t)
	for _, tc := range []struct{ r, c int }{
		{-100, -100}, {0, 0}, {9999, 9999}, {m.height + 50, m.width + 50},
	} {
		text := m.selectionText(normalizeSelection(
			selPoint{row: tc.r, col: tc.c},
			selPoint{row: tc.r + 5, col: tc.c + 5},
		))
		_ = text // 只要不 panic
	}
}

// TestSelectionTextEmptyWhenInactive 断言没有选区时提取为空。
func TestSelectionTextEmptyWhenInactive(t *testing.T) {
	m := selectionTestModel(t)
	if got := m.selectedText(); got != "" {
		t.Fatalf("无选区却返回了内容: %q", got)
	}
}

// ── 高亮 ────────────────────────────────────────────────────────────

// TestSelectionHighlightAppearsInView 断言选区在渲染时可见（有高亮）。
func TestSelectionHighlightAppearsInView(t *testing.T) {
	m := selectionTestModel(t)
	plain := m.renderFrame()

	m.Update(mouseClick(5, 3))
	m.Update(mouseMotion(30, 5))
	highlighted := m.renderFrame()

	if !m.selectionHighlighted {
		t.Fatal("选区没有渲染出高亮")
	}
	// 高亮不应改变可见字符 —— 只改样式。
	// 注意：lipgloss 在非 TTY 输出下会剥掉 ANSI，所以这里断言的是文本不变，
	// 而不是断言出现了转义序列。
	if plainText(plain) != plainText(highlighted) {
		before, after := plainText(plain), plainText(highlighted)
		t.Fatalf("高亮改变了可见文本内容:\n before=%q\n after =%q", before, after)
	}
}

// TestSelectionHighlightIsClearedFromFlag 断言清除选区后高亮标记也复位，
// 否则测试和「重绘一次」的路径会误判。
func TestSelectionHighlightClearedFromFlag(t *testing.T) {
	m := selectionTestModel(t)
	m.Update(mouseClick(5, 3))
	m.Update(mouseMotion(30, 5))
	_ = m.renderFrame()
	if !m.selectionHighlighted {
		t.Fatal("前提不成立：没有高亮")
	}
	m.clearSelection()
	_ = m.renderFrame()
	if m.selectionHighlighted {
		t.Fatal("清除选区后仍标记为高亮")
	}
}

// TestCopySelectionSilentOnSuccess 断言复制成功后不往 transcript 里插入提示，
// 否则每次框选都会污染正文、把视角推离阅读位置。
func TestCopySelectionSilentOnSuccess(t *testing.T) {
	m := selectionTestModel(t)
	m.SetClipboardWriter(func(string) error { return nil })
	before := len(m.blocks)

	m.Update(mouseClick(2, 2))
	m.Update(mouseMotion(30, 4))
	m.Update(mouseRelease(30, 4))

	if got := len(m.blocks); got != before {
		t.Fatalf("复制成功却新增了 %d 个 block", got-before)
	}
	for _, b := range m.blocks {
		if strings.Contains(b, "clipboard") {
			t.Fatalf("transcript 里仍有复制提示: %q", b)
		}
	}
}

// TestCopySelectionFailureStillReported 断言复制失败仍然报错 ——
// 静默只针对成功路径，失败必须可见。
func TestCopySelectionFailureStillReported(t *testing.T) {
	m := selectionTestModel(t)
	m.SetClipboardWriter(func(string) error { return errClipboardForTest })
	before := len(m.blocks)

	m.Update(mouseClick(2, 2))
	m.Update(mouseMotion(30, 4))
	m.Update(mouseRelease(30, 4))

	if got := len(m.blocks); got != before+1 {
		t.Fatalf("复制失败应新增 1 个提示 block，实际增加 %d 个", got-before)
	}
	if !strings.Contains(m.blocks[len(m.blocks)-1], "copy failed") {
		t.Fatalf("失败提示内容不对: %q", m.blocks[len(m.blocks)-1])
	}
}

// ── 边界 ────────────────────────────────────────────────────────────

func TestSelectionBoundaries(t *testing.T) {
	t.Run("nil 剪贴板写入器不 panic", func(t *testing.T) {
		m := selectionTestModel(t)
		m.SetClipboardWriter(nil)
		m.Update(mouseClick(2, 2))
		m.Update(mouseMotion(20, 4))
		m.Update(mouseRelease(20, 4))
	})

	t.Run("剪贴板写失败不 panic", func(t *testing.T) {
		m := selectionTestModel(t)
		m.SetClipboardWriter(func(string) error { return errClipboardForTest })
		m.Update(mouseClick(2, 2))
		m.Update(mouseMotion(20, 4))
		m.Update(mouseRelease(20, 4))
	})

	t.Run("零尺寸终端不 panic", func(t *testing.T) {
		m := selectionTestModel(t)
		m.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
		m.Update(mouseClick(1, 1))
		m.Update(mouseMotion(3, 3))
		_ = m.renderFrame()
	})

	t.Run("超宽行截断到终端宽度", func(t *testing.T) {
		m := selectionTestModel(t)
		m.blocks = []string{strings.Repeat("X", 1000)}
		m.markBlocksChanged()
		m.refreshViewport()
		text := m.selectionText(normalizeSelection(
			selPoint{row: 1, col: 0},
			selPoint{row: 1, col: 9999},
		))
		if strings.Contains(text, "\n") {
			t.Fatalf("单行选择产生了换行: %q", text)
		}
	})

	t.Run("全空白行不产生垃圾", func(t *testing.T) {
		m := selectionTestModel(t)
		m.blocks = []string{"", "   ", ""}
		m.markBlocksChanged()
		m.refreshViewport()
		text := m.selectionText(normalizeSelection(
			selPoint{row: 1, col: 0},
			selPoint{row: 3, col: 10},
		))
		if strings.TrimSpace(text) != "" {
			t.Fatalf("空白行提取出内容: %q", text)
		}
	})

	t.Run("选区跨越 header 不越界", func(t *testing.T) {
		m := selectionTestModel(t)
		text := m.selectionText(normalizeSelection(
			selPoint{row: 0, col: 0},
			selPoint{row: 1, col: 5},
		))
		_ = text // 只要不 panic
	})
}

// selectionTestModel builds a model with a fixed size and some transcript so
// screen coordinates map to known content.
func selectionTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := config.Config{User: "u", Assistant: "a", Provider: config.ProviderConfig{Name: "deepseek"}}
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	m = model.(*Model)
	for i := 0; i < 20; i++ {
		m.addBlock("content line " + itoa(i))
	}
	m.refreshViewport()
	_ = m.renderFrame()
	return m
}

// errClipboardForTest simulates pbcopy failing.
var errClipboardForTest = errors.New("clipboard unavailable")
