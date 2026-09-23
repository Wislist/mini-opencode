package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/wislist/mini-opencode/internal/agent"
)

// ── 审批提示同样受约束 ─────────────────────────────────────────────

// heredocCall builds the shape that made this visible: a bash command carrying
// a whole file body.
func heredocCall(t *testing.T) *agent.ToolCall {
	t.Helper()
	command := "cat > /tmp/generated.go <<'EOF'\n" +
		strings.Repeat("func generated() { /* a long body line */ }\n", 40) + "EOF\necho done"
	return bashCall(t, command)
}

// TestPermissionPromptCapsLongCommand 断言审批提示不会被一坨命令撑满屏幕 ——
// 提示是交互界面，问题本身必须留在视野里。
func TestPermissionPromptCapsLongCommand(t *testing.T) {
	m := toolCallTestModel(t, 80, 30)
	m.pendingPerm = &permissionRequestMsg{call: *heredocCall(t), resp: make(chan permissionDecision, 1)}
	m.state = statePermission

	prompt := m.renderPermissionPrompt()
	if rows := countRenderedRows(prompt); rows > toolCallMaxRows+boxRowsOverhead+3 {
		t.Fatalf("审批提示渲染了 %d 行，命令没有被约束:\n%s", rows, plainText(prompt))
	}
	plain := plainText(prompt)
	if !strings.Contains(plain, "allow?") {
		t.Fatalf("审批选项被命令挤出视野:\n%s", plain)
	}
	if !strings.Contains(plain, "more lines") {
		t.Fatalf("折叠后没有提示隐藏了多少行:\n%s", plain)
	}
}

// TestPermissionPromptKeepsShortCommandIntact 断言普通命令在审批提示里完整显示。
func TestPermissionPromptKeepsShortCommandIntact(t *testing.T) {
	const command = "go test ./internal/tui -count=1"
	m := toolCallTestModel(t, 120, 30)
	m.pendingPerm = &permissionRequestMsg{call: *bashCall(t, command), resp: make(chan permissionDecision, 1)}
	m.state = statePermission

	plain := plainText(m.renderPermissionPrompt())
	if !strings.Contains(plain, command) {
		t.Fatalf("短命令在审批提示里不完整:\n%s", plain)
	}
	if strings.Contains(plain, "more lines") {
		t.Fatalf("短命令被折叠了:\n%s", plain)
	}
}

// TestPermissionPromptFitsTerminal 断言审批提示不溢出终端宽度。
func TestPermissionPromptFitsTerminal(t *testing.T) {
	for _, w := range []int{160, 80, 40, 20, 8} {
		m := toolCallTestModel(t, w, 30)
		m.pendingPerm = &permissionRequestMsg{call: *heredocCall(t), resp: make(chan permissionDecision, 1)}
		m.state = statePermission
		for i, line := range strings.Split(m.renderPermissionPrompt(), "\n") {
			if got := ansi.StringWidth(line); got > m.width {
				t.Fatalf("w=%d 行 %d 宽 %d 超过终端: %q", w, i, got, ansi.Strip(line))
			}
		}
	}
}

// TestToolCallDetailBodyDegenerateWidths 覆盖极窄宽度：折叠后的行数上限仍然成立，
// 且不会 panic。
func TestToolCallDetailBodyDegenerateWidths(t *testing.T) {
	long := strings.Repeat("abcdefghij ", 200)
	for _, w := range []int{1, 2, 3, 5, 8, 12} {
		body := toolCallDetailBody(long, w)
		rows := countRenderedRows(body)
		if rows > toolCallMaxRows {
			t.Fatalf("width=%d: 折叠后 %d 行，超过上限 %d", w, rows, toolCallMaxRows)
		}
		if strings.TrimSpace(body) == "" {
			t.Fatalf("width=%d: 折叠后内容为空", w)
		}
		if !strings.Contains(plainText(body), "⋯") {
			t.Fatalf("width=%d: 折叠后没有省略标记", w)
		}
	}
}

// TestMeasureTextWidthMatchesRenderedLayout 断言宽度测量与实际渲染一致。
// 布局算法（renderFrame）按 ansi.StringWidth 计数，而样式片段若用 lipgloss.Width
// 测量会在无颜色 profile 下少算一个「…」，两边必须用同一个函数。
func TestMeasureTextWidthMatchesRenderedLayout(t *testing.T) {
	cases := []string{
		"plain ascii",
		"中文标题",
		"mixed 中文 and ascii",
		dimStyle.Render("⋯ (3 more lines)"),
		codeOmittedStyle.Render("⋯ (10 more lines)"),
	}
	for _, c := range cases {
		if got, want := measureTextWidth(c), ansi.StringWidth(lipgloss.NewStyle().Render(c)); got != want {
			t.Fatalf("measureTextWidth(%q) = %d, want %d", c, got, want)
		}
	}
}
