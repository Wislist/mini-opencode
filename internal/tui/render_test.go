package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/wislist/mini-opencode/internal/config"
)

func TestWordWrapUsesTerminalCellWidth(t *testing.T) {
	const width = 10
	text := "这是一个很长的中文回答不会使用空格所以必须换行"

	wrapped := wordWrap(text, width)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Fatalf("wordWrap() did not wrap CJK text: %q", wrapped)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestWordWrapPreservesANSISequences(t *testing.T) {
	const width = 8
	text := lipgloss.NewStyle().Bold(true).Render("中文中文中文中文中文")

	for i, line := range strings.Split(wordWrap(text, width), "\n") {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("styled line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestViewFillsTerminalHeight(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	m.addBlock(m.renderAssistantMessage("一段用于测试终端布局的回答"))

	const width, height = 78, 24
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: width, Height: height}); cmd != nil {
		t.Fatal("WindowSizeMsg returned an unexpected command")
	}

	view := m.View()
	if m.width != width-1 {
		t.Fatalf("model width = %d, want %d to reserve the terminal's final cell", m.width, width-1)
	}
	if m.viewport.Width != width-1 {
		t.Fatalf("viewport width = %d, want %d", m.viewport.Width, width-1)
	}
	if got := lipgloss.Height(view); got != height {
		t.Fatalf("View() height = %d, want %d", got, height)
	}
	for i, line := range strings.Split(view, "\n") {
		if got := ansi.StringWidth(line); got > width-1 {
			t.Fatalf("view line %d width = %d, want <= %d: %q", i, got, width-1, line)
		}
	}
}

func TestMarkdownHeadingColors(t *testing.T) {
	tests := []struct {
		level int
		want  lipgloss.TerminalColor
	}{
		{level: 1, want: colorRed},
		{level: 2, want: colorBlue},
		{level: 3, want: colorAccent},
		{level: 4, want: colorYellow},
	}
	for _, tt := range tests {
		if got := markdownHeadingStyle(tt.level).GetForeground(); got != tt.want {
			t.Fatalf("heading level %d color = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestRenderAssistantMarkdownBoxesAndCollapsesLongCode(t *testing.T) {
	var code []string
	for i := 1; i <= 20; i++ {
		code = append(code, fmt.Sprintf("line-%02d: %s", i, strings.Repeat("x", 80)))
	}
	text := "# 一级标题\n## 二级标题\n### 三级标题\n\n```go\n" + strings.Join(code, "\n") + "\n```"

	const width = 40
	rendered := renderAssistantMarkdown(text, width)
	plain := ansi.Strip(rendered)
	for _, want := range []string{"# 一级标题", "## 二级标题", "### 三级标题", "GO", "╭", "╰", "(10 lines hidden)", "line-01", "line-20"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered markdown missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "line-09") {
		t.Fatalf("collapsed code unexpectedly contains a hidden middle line:\n%s", plain)
	}
	for i, line := range strings.Split(rendered, "\n") {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("rendered line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestRenderAssistantMarkdownStylesInlineCode(t *testing.T) {
	rendered := renderAssistantMarkdown("使用 `go test ./...` 验证。", 60)
	plain := ansi.Strip(rendered)
	if strings.Contains(plain, "`") {
		t.Fatalf("inline code still contains Markdown delimiters: %q", plain)
	}
	if !strings.Contains(plain, "go test ./...") {
		t.Fatalf("inline code content missing: %q", plain)
	}
}

func TestAssistantMarkdownNeverExceedsModelWidth(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	m.width = 50
	text := "# 标题\n\n```go\n" + strings.Repeat("veryLongIdentifier", 12) + "\n```"

	rendered := m.renderAssistantMessage(text)
	for i, line := range strings.Split(rendered, "\n") {
		if got := ansi.StringWidth(line); got > m.width {
			t.Fatalf("assistant line %d width = %d, want <= %d: %q", i, got, m.width, line)
		}
	}
}

func TestRenderAssistantMarkdownRendersBoldWithoutMarkers(t *testing.T) {
	rendered := renderAssistantMarkdown("普通文字 **优先测公共接口** 结尾", 60)
	plain := ansi.Strip(rendered)
	if strings.Contains(plain, "**") {
		t.Fatalf("bold Markdown markers were not removed: %q", plain)
	}
	if !strings.Contains(plain, "优先测公共接口") {
		t.Fatalf("bold content missing: %q", plain)
	}
}

func TestRenderAssistantMarkdownDropsUnmatchedBoldMarker(t *testing.T) {
	rendered := renderAssistantMarkdown("**无法闭合的加粗", 60)
	plain := ansi.Strip(rendered)
	if plain != "无法闭合的加粗" {
		t.Fatalf("unmatched bold marker rendered as %q", plain)
	}
}

func TestMarkdownFourthLevelHeadingIsYellow(t *testing.T) {
	level, ok := markdownHeadingLevel("#### 黄色标题")
	if !ok || level != 4 {
		t.Fatalf("markdownHeadingLevel() = (%d, %v), want (4, true)", level, ok)
	}
	if got := markdownHeadingStyle(level).GetForeground(); got != colorYellow {
		t.Fatalf("fourth-level heading color = %v, want %v", got, colorYellow)
	}
}
