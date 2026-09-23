package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/session"
)

// The context/version indicator lives in the header's right-hand side, and the
// command hints live in the help bar below the input. These tests pin both, plus
// the degradation rules that keep the header to a single row.

func statusTestModel(t *testing.T, w, h int) *Model {
	t.Helper()
	cfg := config.Config{
		User:      "u",
		Assistant: "a",
		Provider: config.ProviderConfig{
			Name:          "deepseek",
			Model:         "deepseek-chat",
			ContextWindow: 128000,
		},
	}
	m := New(&cfg, t.TempDir(), "0.4.0")
	model, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return model.(*Model)
}

// withConversation gives the model a session and some messages, so the context
// indicator has both a system floor and a conversation to report.
func withConversation(t *testing.T, m *Model) *Model {
	t.Helper()
	m.SetSessionStore(session.NewStore(t.TempDir()))
	m.currentSession = m.sessions.Create("t")
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}, agent.WithContextWindow(128000)))
	m.runtime.SetMessages([]agent.Message{{Role: agent.RoleUser, Content: strings.Repeat("x", 400)}})
	return m
}

// TestHeaderShowsContextVersionAndModel 断言 header 右侧给出 ctx、版本号与 provider。
func TestHeaderShowsContextVersionAndModel(t *testing.T) {
	m := withConversation(t, statusTestModel(t, 120, 30))

	header := plainText(m.renderHeader())
	for _, want := range []string{"ctx", "0.4.0", "deepseek", "chat"} {
		if !strings.Contains(header, want) {
			t.Errorf("header 缺少 %q: %q", want, header)
		}
	}
}

// TestHeaderHidesChatWhenEmpty 断言没有对话时不显示 chat 计数，
// 避免显示无意义的「0 chat」。
func TestHeaderHidesChatWhenEmpty(t *testing.T) {
	m := statusTestModel(t, 120, 30)
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}, agent.WithContextWindow(128000)))

	if header := plainText(m.renderHeader()); strings.Contains(header, "chat") {
		t.Fatalf("空对话却显示了 chat 计数: %q", header)
	}
}

// TestHeaderIsAlwaysOneRow 断言 header 在任意宽度下都只占一行。
// 多行输入产生的标题曾经带换行，把单行 header 撑成好几行。
func TestHeaderIsAlwaysOneRow(t *testing.T) {
	for _, w := range []int{160, 120, 80, 60, 40, 30, 20, 12, 8, 4, 2, 1} {
		m := withConversation(t, statusTestModel(t, w, 30))
		m.currentSession.Title = "多行标题\n第二行\t带制表符的标题"
		if rows := lipgloss.Height(m.renderHeader()); rows != 1 {
			t.Fatalf("w=%d: header 占 %d 行: %q", w, rows, plainText(m.renderHeader()))
		}
	}
}

// TestHeaderFitsTerminalWidth 断言 header 不溢出终端宽度。
func TestHeaderFitsTerminalWidth(t *testing.T) {
	for _, w := range []int{160, 120, 80, 60, 40, 30, 20, 12, 8, 4, 2, 1} {
		m := withConversation(t, statusTestModel(t, w, 30))
		m.currentSession.Title = strings.Repeat("很长很长的会话标题", 8)
		if got := lipgloss.Width(m.renderHeader()); got > m.width {
			t.Fatalf("w=%d: header 宽 %d 溢出: %q", w, got, plainText(m.renderHeader()))
		}
	}
}

// TestHeaderKeepsSessionTitleOnNarrowTerminal 断言窄终端下会话标题被缩短而不是丢弃 ——
// 标题是识别会话的东西。
func TestHeaderKeepsSessionTitleOnNarrowTerminal(t *testing.T) {
	m := withConversation(t, statusTestModel(t, 60, 30))
	m.currentSession.Title = "refactor the streaming renderer"

	header := plainText(m.renderHeader())
	if !strings.Contains(header, "refactor") {
		t.Fatalf("窄终端下会话标题被整段丢弃: %q", header)
	}
}

// TestHeaderSurvivesNilRuntime 断言没有 runtime 时不 panic。
func TestHeaderSurvivesNilRuntime(t *testing.T) {
	m := statusTestModel(t, 100, 30)
	m.SetRuntime(nil)

	header := plainText(m.renderHeader())
	if header == "" {
		t.Fatal("无 runtime 时 header 为空")
	}
	if !strings.Contains(header, "0.4.0") {
		t.Fatalf("无 runtime 时仍应显示版本号: %q", header)
	}
	if !strings.Contains(header, "ctx") {
		t.Fatalf("无 runtime 时仍应显示 ctx 段: %q", header)
	}
}

// TestHelpBarKeepsRunningHints 断言运行中的 spinner 与 esc 提示仍在，
// 状态栏替换的是命令列表而不是这些即时反馈。
func TestHelpBarKeepsRunningHints(t *testing.T) {
	m := statusTestModel(t, 120, 30)
	m.state = stateRunning

	bar := plainText(m.renderHelpBar())
	if !strings.Contains(bar, "esc to interrupt") {
		t.Fatalf("运行中缺少中断提示: %q", bar)
	}
}

// TestHelpBarFitsTerminalWidth 断言帮助栏在任意宽度下都不溢出。
func TestHelpBarFitsTerminalWidth(t *testing.T) {
	for _, w := range []int{160, 120, 100, 80, 60, 40, 30, 20, 8, 4, 1} {
		m := statusTestModel(t, w, 30)
		if got := lipgloss.Width(m.renderHelpBar()); got > m.width {
			t.Fatalf("w=%d: 帮助栏宽 %d 溢出终端: %q", w, got, plainText(m.renderHelpBar()))
		}
	}
}

// TestHelpBarAlwaysOffersACommand 断言缩到极限也仍留 /help ——
// 用户看到 /help 才知道有命令可查。
func TestHelpBarAlwaysOffersACommand(t *testing.T) {
	for _, w := range []int{120, 80, 40, 20, 12, 8} {
		m := statusTestModel(t, w, 30)
		if bar := plainText(m.renderHelpBar()); !strings.Contains(bar, "/help") {
			t.Fatalf("w=%d: 帮助栏丢掉了 /help: %q", w, bar)
		}
	}
}

// TestHeaderPrecedesViewportAndHelpBarFollowsInput 断言面板顺序：
// header → 正文 → 输入框 → 帮助栏。
func TestHeaderPrecedesViewportAndHelpBarFollowsInput(t *testing.T) {
	m := statusTestModel(t, 100, 30)
	m.addBlock("transcript content")

	view := plainText(m.renderFrame())
	headerIdx := strings.Index(view, "mini-opencode")
	bodyIdx := strings.Index(view, "transcript content")
	inputIdx := strings.Index(view, "❯")

	if headerIdx < 0 || bodyIdx < 0 || inputIdx < 0 {
		t.Fatalf("帧里找不到 header/正文/输入框:\n%s", view)
	}
	if !(headerIdx < bodyIdx && bodyIdx < inputIdx) {
		t.Fatalf("面板顺序不对: header=%d body=%d input=%d", headerIdx, bodyIdx, inputIdx)
	}
}
