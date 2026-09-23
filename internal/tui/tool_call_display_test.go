package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

func toolCallTestModel(t *testing.T, w, h int) *Model {
	t.Helper()
	cfg := config.Config{User: "u", Assistant: "a", Provider: config.ProviderConfig{Name: "deepseek"}}
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return model.(*Model)
}

func bashCall(t *testing.T, command string) *agent.ToolCall {
	t.Helper()
	args, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return &agent.ToolCall{ID: "call-1", Name: "bash", Arguments: args}
}

// countRenderedRows returns the rows a block occupies once rendered.
func countRenderedRows(block string) int {
	if block == "" {
		return 0
	}
	return strings.Count(block, "\n") + 1
}

// boxRowsOverhead is the cost of the box around a tool call: the two border
// rows plus the row naming the tool. A body capped at toolCallMaxRows therefore
// renders as toolCallMaxRows + boxRowsOverhead rows.
const boxRowsOverhead = 3

// ── 需求：一条很长的命令不能把对话区域吃掉 ─────────────────────────

// TestLongCommandCallIsCapped 断言一条超长命令只占固定的几行，
// 而不是把整个对话区域推出屏幕。
func TestLongCommandCallIsCapped(t *testing.T) {
	m := toolCallTestModel(t, 80, 30)

	command := strings.Join([]string{
		"cd /Users/someone/project && go test ./internal/tui/... -run 'TestSomethingVeryLong' -count=1 -v 2>&1 | tail -n 50",
		"echo 'first part'",
		"echo 'second part'",
		"echo 'third part'",
		"echo 'fourth part'",
		"echo 'fifth part'",
		"echo 'sixth part'",
		"echo 'seventh part'",
	}, " && ")

	block := m.renderToolCall(bashCall(t, command))
	rows := countRenderedRows(block)
	if rows > toolCallMaxRows+boxRowsOverhead {
		t.Fatalf("长命令渲染了 %d 行，超过上限 %d:\n%s", rows, toolCallMaxRows+boxRowsOverhead, plainText(block))
	}
	if rows < 3 {
		t.Fatalf("长命令渲染了 %d 行，短于「名字 + 至少一行内容」: %s", rows, plainText(block))
	}
}

// TestLongCommandCallReportsHiddenLines 断言被折叠的部分有明确标记，
// 否则截断会被误读成完整命令。
func TestLongCommandCallReportsHiddenLines(t *testing.T) {
	m := toolCallTestModel(t, 80, 30)
	lines := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("echo 'command segment number %02d'", i))
	}
	block := plainText(m.renderToolCall(bashCall(t, strings.Join(lines, " && "))))

	if !strings.Contains(block, "more lines") {
		t.Fatalf("折叠后没有提示隐藏了多少行:\n%s", block)
	}
	if !strings.Contains(block, "command segment number 00") {
		t.Fatalf("开头的内容被丢掉了:\n%s", block)
	}
}

// TestShortCommandCallIsNotTruncated 断言短命令原样显示 —— 折叠不能误伤常见情况。
func TestShortCommandCallIsNotTruncated(t *testing.T) {
	m := toolCallTestModel(t, 80, 30)
	block := plainText(m.renderToolCall(bashCall(t, "go test ./...")))
	if !strings.Contains(block, "bash") || !strings.Contains(block, "go test ./...") {
		t.Fatalf("短命令显示不完整:\n%s", block)
	}
	if strings.Contains(block, "more lines") {
		t.Fatalf("短命令被折叠了:\n%s", block)
	}
}

// TestToolCallNeverExceedsTerminalWidth 断言长命令每个字都还在宽度内，
// 不会把 box 撑破。
func TestToolCallNeverExceedsTerminalWidth(t *testing.T) {
	for _, w := range []int{160, 80, 60, 40, 24, 12} {
		m := toolCallTestModel(t, w, 30)
		command := strings.Repeat("verylongsegment-", 20)
		block := m.renderToolCall(bashCall(t, command))
		for i, line := range strings.Split(block, "\n") {
			if got := ansi.StringWidth(line); got > m.width {
				t.Fatalf("w=%d 行 %d 宽 %d 超过终端: %q", w, i, got, ansi.Strip(line))
			}
		}
	}
}

// TestToolCallRowsAreBoundedAcrossTerminalWidths 断言更窄的终端下
// 折叠上限仍然生效（折行会把行数放大）。
func TestToolCallRowsAreBoundedAcrossTerminalWidths(t *testing.T) {
	for _, w := range []int{120, 80, 40} {
		m := toolCallTestModel(t, w, 30)
		command := strings.Repeat("echo 'a fairly long command segment here' && ", 10)
		rows := countRenderedRows(m.renderToolCall(bashCall(t, command)))
		if rows > toolCallMaxRows+boxRowsOverhead {
			t.Fatalf("w=%d: 渲染了 %d 行，超过上限 %d", w, rows, toolCallMaxRows+boxRowsOverhead)
		}
	}
}

// ── 边界 ───────────────────────────────────────────────────────────

func TestToolCallDisplayBoundaries(t *testing.T) {
	t.Run("空参数不 panic", func(t *testing.T) {
		m := toolCallTestModel(t, 80, 30)
		call := &agent.ToolCall{Name: "bash", Arguments: json.RawMessage(`{}`)}
		if got := m.renderToolCall(call); got == "" {
			t.Fatal("空参数渲染为空")
		}
	})

	t.Run("非 JSON 参数按原文显示", func(t *testing.T) {
		m := toolCallTestModel(t, 80, 30)
		call := &agent.ToolCall{Name: "bash", Arguments: json.RawMessage(`not json at all`)}
		if got := plainText(m.renderToolCall(call)); !strings.Contains(got, "not json at all") {
			t.Fatalf("非 JSON 参数被吞掉: %q", got)
		}
	})

	t.Run("刚好在上限上的命令不折叠", func(t *testing.T) {
		m := toolCallTestModel(t, 200, 30)
		var lines []string
		for i := 0; i < toolCallMaxRows; i++ {
			lines = append(lines, fmt.Sprintf("c%02d", i))
		}
		block := plainText(m.renderToolCall(bashCall(t, strings.Join(lines, " && "))))
		if strings.Contains(block, "more lines") {
			t.Fatalf("刚好 %d 行的命令被折叠了:\n%s", toolCallMaxRows, block)
		}
	})

	t.Run("超过上限即折叠", func(t *testing.T) {
		m := toolCallTestModel(t, 200, 30)
		// Each clause is a separate wrapped row at this width, so the command
		// needs more rows than the cap allows.
		var lines []string
		for i := 0; i < toolCallMaxRows+1; i++ {
			lines = append(lines, fmt.Sprintf("c%02d", i))
		}
		if rows := strings.Count(wordWrap(strings.Join(lines, "\n"), 196), "\n") + 1; rows <= toolCallMaxRows {
			t.Fatalf("测试前提不成立：命令只有 %d 行", rows)
		}
		block := plainText(m.renderToolCall(bashCall(t, strings.Join(lines, "\n"))))
		if !strings.Contains(block, "more lines") {
			t.Fatalf("%d 行的命令没有折叠:\n%s", toolCallMaxRows+1, block)
		}
	})

	t.Run("单行超长命令仍受行数上限约束", func(t *testing.T) {
		m := toolCallTestModel(t, 40, 30)
		block := m.renderToolCall(bashCall(t, strings.Repeat("x", 4000)))
		if rows := countRenderedRows(block); rows > toolCallMaxRows+boxRowsOverhead {
			t.Fatalf("单行超长命令渲染了 %d 行，超过上限 %d", rows, toolCallMaxRows+boxRowsOverhead)
		}
	})

	t.Run("多次渲染同一调用结果一致", func(t *testing.T) {
		m := toolCallTestModel(t, 80, 30)
		call := bashCall(t, strings.Repeat("echo hi && ", 50))
		if first, second := m.renderToolCall(call), m.renderToolCall(call); first != second {
			t.Fatal("重复渲染同一调用结果不一致")
		}
	})
}
