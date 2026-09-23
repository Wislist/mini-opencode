package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

func toolCall(args map[string]any) *agent.ToolCall {
	raw, _ := json.Marshal(args)
	return &agent.ToolCall{Name: "bash", Arguments: raw}
}

// bash 调用的展示应当简洁：命令本身可以很长，但转录里不该把整串环境变量
// 和管道全部铺开。
//
// 之前用 `command: <完整命令>`，一个带 export 的长命令会占满 9 行，把正文挤走。

// TestBashCallStaysWithinRowCap 断言 bash 调用不会把整串环境和管道铺满屏幕。
//
// 上限是 toolCallMaxRows + 边框，而不是"永远一行"：多行命令仍保留可读的几行，
// 但转录不该被一次调用的参数占掉大半屏。
func TestBashCallStaysWithinRowCap(t *testing.T) {
	m := inputTestModel(t)
	long := `cd /Users/wislist/Desktop/worksplace/mini-opencode && export GOCACHE=$PWD/.gocache ` +
		`GOMODCACHE=/Users/wislist/go/pkg/mod GOFLAGS=-mod=mod GOSUMDB=off GOPROXY=off && ` +
		`go test ./... -count=1 2>&1 | tail -20`

	out := m.renderToolCall(toolCall(map[string]any{"command": long}))
	// 边框 2 行 + 工具名 1 行 + 内容上限 toolCallMaxRows
	if got := lineCount(out); got > toolCallMaxRows+3 {
		t.Fatalf("bash 调用占 %d 行，上限 %d:\n%s", got, toolCallMaxRows+3, plainText(out))
	}
}

// TestBashCallKeepsCommandHead 断言摘要保留了命令开头，
// 否则用户认不出这是什么命令。
func TestBashCallKeepsCommandHead(t *testing.T) {
	m := inputTestModel(t)
	out := plainText(m.renderToolCall(toolCall(map[string]any{
		"command": "go test ./internal/agent -run TestRuntimeRun -count=1 -v 2>&1 | head -50",
	})))
	if !strings.Contains(out, "go test") {
		t.Fatalf("摘要丢失了命令开头:\n%s", out)
	}
}

// TestBashCallMarksTruncation 断言被截断时有省略提示，
// 不能让读者以为看到的就是全部。
func TestBashCallMarksTruncation(t *testing.T) {
	m := inputTestModel(t)
	long := "echo " + strings.Repeat("very-long-argument ", 40)
	out := plainText(m.renderToolCall(toolCall(map[string]any{"command": long})))
	// The fold marker is "⋯ (N more lines)", not a plain ellipsis.
	if !strings.Contains(out, "more lines") {
		t.Fatalf("截断没有提示:\n%s", out)
	}
}

// TestShortBashCallIsNotTruncated 断言短命令原样显示 ——
// 摘要不能以牺牲完整性为代价。
func TestShortBashCallIsNotTruncated(t *testing.T) {
	m := inputTestModel(t)
	out := plainText(m.renderToolCall(toolCall(map[string]any{"command": "go build ./..."})))
	if !strings.Contains(out, "go build ./...") {
		t.Fatalf("短命令被改动了:\n%s", out)
	}
	if strings.Contains(out, "…") {
		t.Fatalf("短命令不该有省略号:\n%s", out)
	}
}

// TestNonBashToolsKeepTheirDetail 断言其它工具的参数仍然完整显示 ——
// 只有 bash 的命令过长时才需要摘要。
func TestNonBashToolsKeepTheirDetail(t *testing.T) {
	m := inputTestModel(t)
	raw, _ := json.Marshal(map[string]any{"path": "internal/tui/render.go", "old_string": "a", "new_string": "b"})
	out := plainText(m.renderToolCall(&agent.ToolCall{Name: "edit", Arguments: raw}))
	if !strings.Contains(out, "internal/tui/render.go") {
		t.Fatalf("edit 的路径没有显示:\n%s", out)
	}
}

// TestToolCallDetailBoundaries 覆盖边界。
func TestToolCallDetailBoundaries(t *testing.T) {
	m := inputTestModel(t)

	t.Run("空命令", func(t *testing.T) {
		out := m.renderToolCall(toolCall(map[string]any{"command": ""}))
		if lineCount(out) > 4 {
			t.Fatalf("空命令占了 %d 行", lineCount(out))
		}
	})

	t.Run("只有换行的命令", func(t *testing.T) {
		out := m.renderToolCall(toolCall(map[string]any{"command": "\n\n\n"}))
		if lineCount(out) > 4 {
			t.Fatalf("多行空命令占了 %d 行", lineCount(out))
		}
	})

	t.Run("多行命令受行数上限约束", func(t *testing.T) {
		var lines []string
		for i := 0; i < 50; i++ {
			lines = append(lines, fmt.Sprintf("line %02d", i))
		}
		out := plainText(m.renderToolCall(toolCall(map[string]any{
			"command": strings.Join(lines, "\n"),
		})))
		if !strings.Contains(out, "line 00") {
			t.Fatalf("开头没有显示:\n%s", out)
		}
		if strings.Contains(out, "line 49") {
			t.Fatalf("50 行命令全部铺开，没有被约束:\n%s", out)
		}
	})

	t.Run("参数不是 JSON", func(t *testing.T) {
		out := m.renderToolCall(&agent.ToolCall{Name: "bash", Arguments: json.RawMessage(`not json`)})
		if lineCount(out) > 4 {
			t.Fatalf("非法参数占了 %d 行:\n%s", lineCount(out), plainText(out))
		}
	})

	t.Run("超窄终端不溢出", func(t *testing.T) {
		narrow := inputTestModel(t)
		narrow.width = 20
		out := narrow.renderToolCall(toolCall(map[string]any{
			"command": strings.Repeat("x", 500),
		}))
		for _, line := range strings.Split(out, "\n") {
			if w := len([]rune(plainText(line))); w > narrow.width+2 {
				t.Fatalf("窄终端下行宽 %d 超出: %q", w, plainText(line))
			}
		}
	})
}
