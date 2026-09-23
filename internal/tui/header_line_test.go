package tui

import (
	"charm.land/lipgloss/v2"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/session"
)

func headerTestModel(t *testing.T, width int) *Model {
	t.Helper()
	cfg := config.Config{
		User:      "u",
		Assistant: "a",
		Provider:  config.ProviderConfig{Name: "deepseek", ContextWindow: 600000},
	}
	m := New(&cfg, t.TempDir(), "0.4.0")
	model, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	return model.(*Model)
}

// TestHeaderIsAlwaysOneLine 断言信息栏永远只占一行。
//
// 它曾经被会话标题撑成多行：首条消息是多行输入时，标题里带着换行符，而标题
// 只按长度截断，换行原样进入渲染。
func TestHeaderIsAlwaysOneLine(t *testing.T) {
	titles := []string{
		"简单标题",
		"你好\n你好\n\n你好\n你好",
		"\n\n前导换行",
		"尾随换行\n",
		"a\nb\r\nc",
		strings.Repeat("很长", 40),
		strings.Repeat("long ", 40),
		strings.Repeat("x\n", 50),
		"",
	}

	for _, title := range titles {
		for _, width := range []int{200, 140, 120, 100, 80, 60, 40, 20} {
			m := headerTestModel(t, width)
			m.gitStatus = GitStatus{Branch: "main", Modified: 37, Untracked: 19, Available: true}
			m.currentSession = &session.Session{ID: "s1", Title: title}

			hdr := m.renderHeader()
			if rows := strings.Count(hdr, "\n") + 1; rows != 1 {
				t.Fatalf("title=%q width=%d: header 占 %d 行，应为 1 行:\n%s",
					title, width, rows, plainText(hdr))
			}
		}
	}
}

// TestHeaderNeverExceedsWidth 断言信息栏不会超出终端宽度而触发换行。
func TestHeaderNeverExceedsWidth(t *testing.T) {
	titles := []string{
		"普通标题",
		strings.Repeat("很长很长的标题", 20),
		strings.Repeat("very long title ", 20),
	}
	for _, title := range titles {
		for _, width := range []int{200, 140, 120, 100, 90, 80, 70, 60, 50, 40, 30, 20, 10} {
			m := headerTestModel(t, width)
			m.gitStatus = GitStatus{Branch: "main", Modified: 37, Untracked: 19, Available: true}
			m.currentSession = &session.Session{ID: "s1", Title: title}

			hdr := m.renderHeader()
			if got := displayWidth(hdr); got > m.width {
				t.Fatalf("title=%q width=%d: header 宽 %d 超过 %d:\n%s",
					title, width, got, m.width, plainText(hdr))
			}
		}
	}
}

// TestSessionTitleIsSingleLine 断言标题在存储层就被压成一行，
// 而不是等到渲染时才处理 —— 否则会话列表等处也会出现同样的问题。
func TestSessionTitleIsSingleLine(t *testing.T) {
	cases := map[string]string{
		"你好\n你好\n\n你好": "你好 你好 你好",
		"\n\n前导":       "前导",
		"尾随\n":         "尾随",
		"a\nb\r\nc":    "a b c",
		"单行":           "单行",
		"":             "new session",
		"   ":          "new session",
		// Tabs are whitespace too: collapsing them keeps the status bar from
		// reserving an unpredictable number of cells.
		"a\tb": "a b",
	}
	for in, want := range cases {
		if got := session.TitleFromMessage(in); got != want {
			t.Errorf("TitleFromMessage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTruncateTitleAddsEllipsis 断言过长的标题用省略号收尾，
// 而不是被硬切或换行。
func TestTruncateTitleAddsEllipsis(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"this is too long", 10, "this is t…"},
		{"abcdefghijk", 5, "abcd…"},
		{"", 5, ""},
		{"中文标题很长很长", 4, "中文标…"},
	}
	for _, tc := range cases {
		if got := truncateTitle(tc.in, tc.max); got != tc.want {
			t.Errorf("truncateTitle(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

// TestTruncateTitleStripsNewlines 断言即使调用方漏了处理，截断也不会让换行漏出去。
func TestTruncateTitleStripsNewlines(t *testing.T) {
	got := truncateTitle("line one\nline two", 30)
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("truncateTitle 保留了换行: %q", got)
	}
}

// TestHeaderSurvivesZeroWidth 覆盖零宽度终端，避免除零或 panic。
func TestHeaderSurvivesZeroWidth(t *testing.T) {
	m := headerTestModel(t, 0)
	m.currentSession = &session.Session{ID: "s1", Title: "标题"}
	_ = m.renderHeader()
}

// displayWidth is the widest line of a rendered block, in terminal cells.
func displayWidth(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if w := lipgloss.Width(line); w > max {
			max = w
		}
	}
	return max
}
