package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/config"
)

func perfTestModel(t *testing.T, w, h int) *Model {
	t.Helper()
	cfg := config.Config{User: "u", Assistant: "a", Provider: config.ProviderConfig{Name: "deepseek"}}
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return model.(*Model)
}

func fillTranscript(m *Model, n int) {
	for i := 0; i < n; i++ {
		m.addBlock(fmt.Sprintf("block %02d %s", i, strings.Repeat("x", 60)))
	}
	m.refreshViewport()
}

// ── 1. 工具调用时不卡 ──────────────────────────────────────────────

// TestToolCallFrameCostIsFlat 断言「追加一个工具结果」的成本不随已有内容增长。
//
// 卡顿的根因是每逢工具事件都调用 viewport.SetContent，而它内部会
// ReplaceAll + Split + 算最长行，全是 O(总字符数)。正文是只追加的，
// 每次只多一两块却把整份内容重新切分一遍。
func TestToolCallFrameCostIsFlat(t *testing.T) {
	small := perfTestModel(t, 120, 40)
	fillTranscript(small, 50)
	large := perfTestModel(t, 120, 40)
	fillTranscript(large, 800)

	// 断言「推进 viewport 的字节数」不随历史增长。直接测耗时会有 flake，
	// 而这个计数正是卡顿的来源：SetContent 的成本与传入的字节数成正比。
	smallCost := measureAppendWork(small)
	largeCost := measureAppendWork(large)
	if largeCost > smallCost*2 {
		t.Fatalf("追加成本随历史增长: 50 块=%d 字节, 800 块=%d 字节", smallCost, largeCost)
	}
	// 上限：窗口有界，所以一次追加不该推进超过一整窗口的量。
	if largeCost > 64*1024 {
		t.Fatalf("单次追加推进了 %d 字节，窗口没有生效", largeCost)
	}
}

// measureAppendWork 返回一次追加操作触碰的字节数，用作「是否全量重建」的代理指标。
func measureAppendWork(m *Model) int {
	before := m.viewportRebuildBytes
	m.addBlock("→ tool output")
	m.refreshViewport()
	return m.viewportRebuildBytes - before
}

// ── 2. 生成结论时不强制拉到底 ──────────────────────────────────────

// TestStreamingDoesNotForceScrollToBottom 断言用户上滑阅读历史时，
// 新到达的流式内容不会把视角强行拽回底部。
func TestStreamingDoesNotForceScrollToBottom(t *testing.T) {
	m := perfTestModel(t, 100, 24)
	fillTranscript(m, 200)

	// 用户上滑离开底部
	m.scrollLines(-20)
	if m.viewport.AtBottom() {
		t.Fatal("上滑后仍在底部，测试前提不成立")
	}
	before := m.viewport.YOffset()

	// 此时 agent 继续产出内容
	for i := 0; i < 5; i++ {
		m.appendStreamingDelta("more streamed text ")
		m.flushStreamRender()
	}

	if m.viewport.AtBottom() {
		t.Fatal("流式内容把视角强制拉回了底部")
	}
	if m.viewport.YOffset() != before {
		t.Fatalf("上滑位置被改动: %d -> %d", before, m.viewport.YOffset())
	}
}

// TestNewContentKeepsBottomWhenAlreadyAtBottom 断言用户本来就在底部时，
// 新内容仍然跟着走 —— 修复不能以「不再自动跟随」为代价。
func TestNewContentKeepsBottomWhenAlreadyAtBottom(t *testing.T) {
	m := perfTestModel(t, 100, 24)
	fillTranscript(m, 200)
	m.viewport.GotoBottom()
	if !m.viewport.AtBottom() {
		t.Fatal("前提不成立：不在底部")
	}

	m.addBlock("a new block arrives")
	m.refreshViewport()

	if !m.viewport.AtBottom() {
		t.Fatal("在底部时新内容没有跟随")
	}
}

// ── 3. spinner 不被饿死 ────────────────────────────────────────────

// TestSpinnerKeepsTickingUnderLoad 断言正文很长时 spinner 仍能拿到 tick，
// 这是「thinking 动画卡顿」的直接指标。
func TestSpinnerKeepsTickingUnderLoad(t *testing.T) {
	m := perfTestModel(t, 120, 40)
	fillTranscript(m, 800)
	m.state = stateRunning

	msg := m.spinner.Tick()
	_, cmd := m.Update(msg)
	if cmd == nil {
		t.Fatal("长正文下 spinner 没有重新调度 tick")
	}
}

// ── 4. 边界 ────────────────────────────────────────────────────────

// TestViewportAppendBoundaries 覆盖追加路径的边界条件。
func TestViewportAppendBoundaries(t *testing.T) {
	t.Run("空正文追加第一块", func(t *testing.T) {
		m := perfTestModel(t, 80, 24)
		m.addBlock("first")
		m.refreshViewport()
		if !strings.Contains(m.viewport.View(), "first") {
			t.Fatal("第一块没有渲染")
		}
	})

	t.Run("重复刷新幂等", func(t *testing.T) {
		m := perfTestModel(t, 80, 24)
		fillTranscript(m, 20)
		first := m.viewport.View()
		m.refreshViewport()
		m.refreshViewport()
		if m.viewport.View() != first {
			t.Fatal("无变更时重复刷新改变了内容")
		}
	})

	t.Run("原地改写块后内容更新", func(t *testing.T) {
		m := perfTestModel(t, 80, 24)
		fillTranscript(m, 20)
		// Rewrite the newest block: the viewport only renders the visible
		// screenful pinned to the bottom, so an older block would legitimately
		// be off screen and prove nothing.
		last := len(m.blocks) - 1
		m.blocks[last] = "REPLACED CONTENT"
		m.markBlocksChanged()
		m.refreshViewport()
		if !strings.Contains(m.viewport.View(), "REPLACED") {
			t.Fatal("原地改写没有反映到视图")
		}
		if !strings.Contains(m.transcriptContent(), "REPLACED") {
			t.Fatal("原地改写没有进入待渲染内容")
		}
	})

	t.Run("清空正文", func(t *testing.T) {
		m := perfTestModel(t, 80, 24)
		fillTranscript(m, 50)
		m.blocks = nil
		m.markBlocksChanged()
		m.refreshViewport()
		if v := m.viewport.View(); strings.Contains(v, "block 0") {
			t.Fatalf("清空后仍能看到旧内容: %q", v)
		}
	})

	t.Run("超出尾部窗口上限仍可用", func(t *testing.T) {
		m := perfTestModel(t, 80, 24)
		fillTranscript(m, viewportTailBlocks+50)
		if m.viewport.View() == "" {
			t.Fatal("超出尾部窗口后视图为空")
		}
		content := m.transcriptContent()
		// The window keeps the newest blocks, so the oldest ones must be gone
		// and the most recent must be present.
		newest := fmt.Sprintf("block %02d", len(m.blocks)-1)
		if !strings.Contains(content, newest) {
			t.Fatalf("最新的块 %q 不在窗口内", newest)
		}
		if strings.Contains(content, "block 00 ") {
			t.Fatal("最旧的块仍在窗口内，窗口没有生效")
		}
	})

	t.Run("窗口随追加滚动", func(t *testing.T) {
		m := perfTestModel(t, 80, 24)
		fillTranscript(m, transcriptWindowBlocks*2)
		content := m.transcriptContent()
		lines := strings.Count(content, "\n") + 1
		// The rendered content must stay bounded as the transcript grows.
		if lines > transcriptWindowBlocks*8 {
			t.Fatalf("窗口内容 %d 行，没有随窗口收敛", lines)
		}
	})

	t.Run("视口高度为 1 时不 panic", func(t *testing.T) {
		m := perfTestModel(t, 80, 6)
		fillTranscript(m, 30)
		_ = m.renderFrame()
	})
}

// TestScrollWhileStreamingKeepsOffset 覆盖「生成对话时滑动内容区」：
// 滑动必须立即生效，且不被流式刷新重置。
func TestScrollWhileStreamingKeepsOffset(t *testing.T) {
	m := perfTestModel(t, 100, 24)
	fillTranscript(m, 300)
	m.state = stateRunning

	m.scrollLines(-10)
	offset := m.viewport.YOffset()
	if offset == 0 {
		t.Fatal("上滑没有生效")
	}

	// 流式渲染若干帧
	for i := 0; i < 10; i++ {
		m.appendStreamingDelta("token ")
		m.flushStreamRender()
	}
	if m.viewport.YOffset() != offset {
		t.Fatalf("流式渲染期间滚动位置被重置: %d -> %d", offset, m.viewport.YOffset())
	}
}

// appendStreamingDelta accumulates text the way a runtime delta does, without
// scheduling a timer, so a test can drive the render synchronously.
func (m *Model) appendStreamingDelta(text string) {
	m.streamingText += text
}
