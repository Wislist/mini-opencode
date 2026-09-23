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

func todoTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := config.Config{User: "u", Assistant: "a", Provider: config.ProviderConfig{Name: "deepseek"}}
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return model.(*Model)
}

func setTodos(t *testing.T, m *Model, rendered string) {
	t.Helper()
	m.Update(runtimeEventMsg{event: agent.Event{
		Type:  agent.EventTodosChanged,
		Todos: rendered,
	}})
}

// ── 需求 1：ctrl+t 折叠/展开 ────────────────────────────────────────

// TestCtrlTCollapsesInsteadOfHiding 断言 ctrl+t 把面板折叠成一行摘要，
// 而不是整块消失 —— 折叠后仍能看到「还有多少没做完」。
func TestCtrlTCollapsesInsteadOfHiding(t *testing.T) {
	m := todoTestModel(t)
	setTodos(t, m, "[>] task one\n[ ] task two\n[ ] task three\n(0/3 done)")

	expanded := m.renderTodoPanel()
	if expanded == "" {
		t.Fatal("初始应展开")
	}
	if strings.Contains(expanded, "\n") && len(strings.Split(expanded, "\n")) < 3 {
		t.Fatalf("展开态太短: %q", expanded)
	}

	m.Update(key("ctrl+t"))
	collapsed := m.renderTodoPanel()
	if collapsed == "" {
		t.Fatal("折叠后面板不该消失")
	}
	// The panel is a bordered box, so one content line renders as three rows
	// (top border, content, bottom border). The fold is about content.
	if got := panelContentLines(collapsed); got != 1 {
		t.Fatalf("折叠态应为 1 行内容，实际 %d 行:\n%s", got, collapsed)
	}
	// 折叠态要保留进度信息
	if !strings.Contains(plainText(collapsed), "3") {
		t.Fatalf("折叠态没有显示进度: %q", plainText(collapsed))
	}

	m.Update(key("ctrl+t"))
	if got := m.renderTodoPanel(); got != expanded {
		t.Fatal("再按 ctrl+t 没有恢复展开态")
	}
}

// TestCollapsedPanelIsShorter 断言折叠确实节省了行数。
func TestCollapsedPanelIsShorter(t *testing.T) {
	m := todoTestModel(t)
	setTodos(t, m, "[>] a\n[ ] b\n[ ] c\n[ ] d\n[ ] e\n(0/5 done)")

	before := panelContentLines(m.renderTodoPanel())
	m.Update(key("ctrl+t"))
	after := panelContentLines(m.renderTodoPanel())
	if after >= before {
		t.Fatalf("折叠没有减少行数: %d -> %d", before, after)
	}
	if after != 1 {
		t.Fatalf("折叠后 %d 行内容，want 1", after)
	}
}

// TestCtrlTWorksWhileRunning 断言运行中也能折叠。
func TestCtrlTWorksWhileRunning(t *testing.T) {
	m := todoTestModel(t)
	setTodos(t, m, "[>] working\n(0/1 done)")
	m.state = stateRunning
	m.Update(key("ctrl+t"))
	if !m.todosCollapsed {
		t.Fatal("运行中 ctrl+t 没有生效")
	}
}

// ── 需求 1b：ctrl+t 不往内容区写提示 ─────────────────────────────────

// todosHintInContent reports whether the transcript contains the ctrl+t
// acknowledgement lines. The fold state is already visible from the panel
// itself (a one-line summary vs the full list), so the toggle must not push
// extra rows into the content area.
func todosHintInContent(m *Model) bool {
	text := plainText(strings.Join(m.blocks, "\n"))
	for _, hint := range []string{"todos folded", "todos expanded"} {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

// TestCtrlTDoesNotWriteHintIntoTranscript 断言 ctrl+t 只改面板状态，
// 不往内容区追加任何提示块；折叠/展开各按一次再按回原状，
// 内容区必须逐字回到初始状态。
func TestCtrlTDoesNotWriteHintIntoTranscript(t *testing.T) {
	m := todoTestModel(t)
	setTodos(t, m, "[>] task one\n[ ] task two\n(0/2 done)")

	before := append([]string(nil), m.blocks...)

	m.Update(key("ctrl+t"))
	if !m.todosCollapsed {
		t.Fatal("ctrl+t 没有折叠面板")
	}
	if todosHintInContent(m) {
		t.Fatalf("折叠时往内容区写了提示:\n%s", plainText(strings.Join(m.blocks, "\n")))
	}

	m.Update(key("ctrl+t"))
	if m.todosCollapsed {
		t.Fatal("再按 ctrl+t 没有展开面板")
	}
	if todosHintInContent(m) {
		t.Fatalf("展开时往内容区写了提示:\n%s", plainText(strings.Join(m.blocks, "\n")))
	}

	if len(m.blocks) != len(before) {
		t.Fatalf("ctrl+t 改变了内容区块数: %d -> %d", len(before), len(m.blocks))
	}
	for i := range before {
		if m.blocks[i] != before[i] {
			t.Fatalf("第 %d 块被改动:\nold=%q\nnew=%q", i, before[i], m.blocks[i])
		}
	}
}

// TestCtrlTRunningDoesNotWriteHintIntoTranscript 断言运行中折叠同样不写提示，
// 面板状态本身要正确翻转，而不是靠内容区兜底。
func TestCtrlTRunningDoesNotWriteHintIntoTranscript(t *testing.T) {
	m := todoTestModel(t)
	setTodos(t, m, "[>] working\n(0/1 done)")
	m.state = stateRunning

	before := len(m.blocks)
	m.Update(key("ctrl+t"))

	if !m.todosCollapsed {
		t.Fatal("运行中 ctrl+t 没有生效")
	}
	if todosHintInContent(m) {
		t.Fatalf("运行中折叠往内容区写了提示:\n%s", plainText(strings.Join(m.blocks, "\n")))
	}
	if len(m.blocks) != before {
		t.Fatalf("运行中 ctrl+t 改变了内容区块数: %d -> %d", before, len(m.blocks))
	}
}

// TestCtrlTNoListDoesNotWriteHint 覆盖没有任务列表的边界：
// 此时按 ctrl+t 仍然翻转状态，但内容区不能出现任何提示。
func TestCtrlTNoListDoesNotWriteHint(t *testing.T) {
	m := todoTestModel(t)
	before := len(m.blocks)

	m.Update(key("ctrl+t"))

	if len(m.blocks) != before {
		t.Fatalf("无列表时 ctrl+t 往内容区写了块: %d -> %d", before, len(m.blocks))
	}
	if todosHintInContent(m) {
		t.Fatalf("无列表时内容区出现提示:\n%s", plainText(strings.Join(m.blocks, "\n")))
	}
	if panel := m.renderTodoPanel(); panel != "" {
		t.Fatalf("空列表不该渲染面板: %q", panel)
	}
}

// TestCtrlTIdempotentOnTranscript 断言重复折叠不会累积内容区块，
// 第二次 ctrl+t 之后状态与内容都与第一次相同。
func TestCtrlTIdempotentOnTranscript(t *testing.T) {
	m := todoTestModel(t)
	setTodos(t, m, "[>] a\n[ ] b\n(0/2 done)")

	m.Update(key("ctrl+t"))
	firstBlocks := len(m.blocks)
	m.Update(key("ctrl+t"))
	m.Update(key("ctrl+t"))

	if !m.todosCollapsed {
		t.Fatal("三次 ctrl+t 后应为折叠态")
	}
	if len(m.blocks) != firstBlocks {
		t.Fatalf("重复折叠累积了内容区块: %d -> %d", firstBlocks, len(m.blocks))
	}
}

// ── 需求 2：列表不跨轮次遗留 ────────────────────────────────────────

// TestCompletedTodosDoNotCarryIntoNextTurn 断言已完成的列表在新的一轮里不再出现。
func TestCompletedTodosDoNotCarryIntoNextTurn(t *testing.T) {
	m := todoTestModel(t)
	m.SetSessionStore(session.NewStore(t.TempDir()))
	m.currentSession = m.sessions.Create("t")
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))

	setTodos(t, m, "[x] a\n[x] b\n(2/2 done)")
	// 一轮结束后，用户发下一条消息
	m.startNewTurn("next question")

	if panel := m.renderTodoPanel(); panel != "" {
		t.Fatalf("已完成的列表带进了下一轮:\n%s", panel)
	}
}

// TestIncompleteTodosDoNotCarryIntoNextTurn 断言未完成的列表同样不带进下一轮 ——
// 需求明确要求「无论已完成还是未完成都不应继续带着」。
func TestIncompleteTodosDoNotCarryIntoNextTurn(t *testing.T) {
	m := todoTestModel(t)
	m.SetSessionStore(session.NewStore(t.TempDir()))
	m.currentSession = m.sessions.Create("t")
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))

	setTodos(t, m, "[>] a\n[ ] b\n(0/2 done)")
	m.startNewTurn("next question")

	if panel := m.renderTodoPanel(); panel != "" {
		t.Fatalf("未完成的列表带进了下一轮:\n%s", panel)
	}
}

// TestInterruptedTodosDoNotCarryIntoNextTurn 覆盖 esc 中断后的残留项 ——
// 中断时列表停在 [>]/[ ]，下一轮不应该还挂着。
func TestInterruptedTodosDoNotCarryIntoNextTurn(t *testing.T) {
	m := todoTestModel(t)
	m.SetSessionStore(session.NewStore(t.TempDir()))
	m.currentSession = m.sessions.Create("t")
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))

	setTodos(t, m, "[>] half done task\n[ ] never started\n(0/2 done)")
	m.state = stateRunning

	// 模拟 esc 中断
	m.Update(key("esc"))
	if m.state != stateIdle {
		t.Fatal("esc 没有中断运行")
	}

	m.startNewTurn("try something else")
	if panel := m.renderTodoPanel(); panel != "" {
		t.Fatalf("被中断的列表带进了下一轮:\n%s", panel)
	}
}

// TestTodoDuringSameRunStillShows 断言清除不影响同一轮内的显示 ——
// agent 干活时必须能看到进度。
func TestTodoDuringSameRunStillShows(t *testing.T) {
	m := todoTestModel(t)
	m.SetSessionStore(session.NewStore(t.TempDir()))
	m.currentSession = m.sessions.Create("t")
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))

	m.startNewTurn("do the work")
	setTodos(t, m, "[>] step one\n[ ] step two\n(0/2 done)")
	if panel := m.renderTodoPanel(); panel == "" {
		t.Fatal("同一轮内 todo 应该可见")
	}

	// 同一轮内继续更新，仍要可见
	setTodos(t, m, "[x] step one\n[>] step two\n(1/2 done)")
	if panel := m.renderTodoPanel(); panel == "" {
		t.Fatal("同一轮内更新后 todo 应该可见")
	}
}

// TestClearedTodosSurviveSessionReload 断言清除写回了存储，
// 否则切换会话时旧列表会复活。
func TestClearedTodosSurviveSessionReload(t *testing.T) {
	dir := t.TempDir()
	m := todoTestModel(t)
	store := session.NewStore(dir)
	m.SetSessionStore(store)
	m.currentSession = store.Create("t")
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))

	setTodos(t, m, "[>] stale task\n(0/1 done)")
	// 把列表写进存储，模拟真实路径
	if err := store.SaveTodos(m.currentSession.ID, `[{"content":"stale task","status":"in_progress"}]`); err != nil {
		t.Fatal(err)
	}

	m.startNewTurn("next question")

	// 重新从存储加载（切会话走的就是这条路）
	reloaded := m.todosForSession(m.currentSession.ID)
	if reloaded != "" {
		t.Fatalf("切换会话后旧列表复活: %q", reloaded)
	}
}

// ── 边界 ───────────────────────────────────────────────────────────

func TestTodoCollapseBoundaries(t *testing.T) {
	t.Run("无列表时 ctrl+t 不 panic", func(t *testing.T) {
		m := todoTestModel(t)
		m.Update(key("ctrl+t"))
		if panel := m.renderTodoPanel(); panel != "" {
			t.Fatalf("空列表不该渲染: %q", panel)
		}
	})

	t.Run("折叠状态在新列表中保持", func(t *testing.T) {
		m := todoTestModel(t)
		setTodos(t, m, "[>] a\n(0/1 done)")
		m.Update(key("ctrl+t"))
		// 同一轮内列表被重写
		setTodos(t, m, "[>] a\n[ ] b\n(0/2 done)")
		if !m.todosCollapsed {
			t.Fatal("列表重写把折叠状态重置了")
		}
	})

	t.Run("折叠后清空列表", func(t *testing.T) {
		m := todoTestModel(t)
		setTodos(t, m, "[>] a\n(0/1 done)")
		m.Update(key("ctrl+t"))
		setTodos(t, m, "")
		if panel := m.renderTodoPanel(); panel != "" {
			t.Fatalf("清空后仍渲染: %q", panel)
		}
	})

	t.Run("重复折叠幂等", func(t *testing.T) {
		m := todoTestModel(t)
		setTodos(t, m, "[>] a\n[ ] b\n(0/2 done)")
		m.Update(key("ctrl+t"))
		first := m.renderTodoPanel()
		m.Update(key("ctrl+t"))
		m.Update(key("ctrl+t"))
		if got := m.renderTodoPanel(); got != first {
			t.Fatal("两次折叠后状态不对")
		}
	})

	t.Run("极矮终端下折叠仍占一行", func(t *testing.T) {
		m := todoTestModel(t)
		model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
		m = model.(*Model)
		setTodos(t, m, "[>] a\n[ ] b\n(0/2 done)")
		m.Update(key("ctrl+t"))
		if lines := panelContentLines(m.renderTodoPanel()); lines > 1 {
			t.Fatalf("折叠态 %d 行内容，want <=1", lines)
		}
	})
}

func lineCount(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// panelContentLines counts the content rows of a bordered panel, excluding the
// top and bottom border rows.
func panelContentLines(panel string) int {
	if strings.TrimSpace(panel) == "" {
		return 0
	}
	return max(0, lineCount(panel)-2)
}

// TestHelpBarAdvertisesCtrlT 断言快捷键在界面上有提示，
// 否则用户不会知道面板可以折叠。
func TestHelpBarAdvertisesCtrlT(t *testing.T) {
	t.Run("有任务列表时运行中提示", func(t *testing.T) {
		m := todoTestModel(t)
		setTodos(t, m, "[>] working\n(0/1 done)")
		m.state = stateRunning
		if !strings.Contains(plainText(m.renderHelpBar()), "ctrl+t") {
			t.Fatalf("运行中帮助栏没有提示 ctrl+t: %q", plainText(m.renderHelpBar()))
		}
	})

	t.Run("无任务列表时运行中不提示", func(t *testing.T) {
		m := todoTestModel(t)
		m.state = stateRunning
		if strings.Contains(plainText(m.renderHelpBar()), "ctrl+t") {
			t.Fatal("没有任务列表却提示了 ctrl+t")
		}
	})

	t.Run("空闲时窄终端降级为快捷键提示", func(t *testing.T) {
		// 80 列塞不下「命令表 + 快捷键」，此时应当只留快捷键，
		// 而不是把提示整条丢掉（用户就再也看不到 ctrl+t 了）。
		m := todoTestModel(t) // 宽 80
		bar := plainText(m.renderHelpBar())
		if !strings.Contains(bar, "ctrl+t") {
			t.Fatalf("80 列下没有提示 ctrl+t: %q", bar)
		}
		if w := lipgloss.Width(m.renderHelpBar()); w > m.width {
			t.Fatalf("帮助栏宽 %d 超过终端 %d", w, m.width)
		}
	})

	t.Run("极宽终端同时显示命令与提示", func(t *testing.T) {
		m := todoTestModel(t)
		model, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
		m = model.(*Model)
		bar := plainText(m.renderHelpBar())
		if !strings.Contains(bar, "/quit") || !strings.Contains(bar, "ctrl+t") {
			t.Fatalf("宽终端应同时显示命令与提示: %q", bar)
		}
	})

	t.Run("极窄终端不溢出", func(t *testing.T) {
		m := todoTestModel(t)
		model, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 20})
		m = model.(*Model)
		bar := m.renderHelpBar()
		if w := lipgloss.Width(bar); w > m.width {
			t.Fatalf("帮助栏宽 %d 超过终端 %d: %q", w, m.width, plainText(bar))
		}
	})
}
