package tui

import (
	"fmt"
	"strings"
	"testing"
)

// 完整回溯历史。
//
// 为修「调用工具时卡顿」，viewport 曾被限制成只渲染最后 60 块。性能确实好了，
// 代价是用户上滑再也找不到对话开头——数据还在（m.blocks 完整、SQLite 也完整），
// 只是渲染不出来。这些测试锁住「历史始终可达」这个不变量。

// TestScrollBackReachesFirstBlock 断言滚到顶能看到最开始的内容。
func TestScrollBackReachesFirstBlock(t *testing.T) {
	m := inputTestModel(t)
	m.addBlock("最开始的问题")
	for i := 1; i < 300; i++ {
		m.addBlock(fmt.Sprintf("第 %d 块", i))
	}
	m.refreshViewport()
	m.viewport.GotoTop()

	view := plainText(m.renderFrame())
	if !strings.Contains(view, "最开始的问题") {
		t.Fatalf("滚到顶看不到第一块（共 %d 块）", len(m.blocks))
	}
}

// TestScrollBackCoversWholeTranscript 断言回溯覆盖整个会话，
// 而不是被固定窗口截断。
func TestScrollBackCoversWholeTranscript(t *testing.T) {
	for _, total := range []int{10, 61, 150, 500, 1200} {
		t.Run(fmt.Sprintf("blocks=%d", total), func(t *testing.T) {
			m := inputTestModel(t)
			for i := 0; i < total; i++ {
				m.addBlock(fmt.Sprintf("块编号 %04d", i))
			}
			m.refreshViewport()
			m.viewport.GotoTop()

			view := plainText(m.renderFrame())
			if !strings.Contains(view, "块编号 0000") {
				t.Fatalf("blocks=%d: 顶部看不到第一块", total)
			}
		})
	}
}

// TestScrollBackIsReachableByScrolling 断言逐次上滑能真正走到顶，
// 而不是卡在某个中间位置。
func TestScrollBackIsReachableByScrolling(t *testing.T) {
	m := inputTestModel(t)
	for i := 0; i < 400; i++ {
		m.addBlock(fmt.Sprintf("内容 %04d", i))
	}
	m.refreshViewport()

	// 从上往下数：反复上滑直到位置不再变化
	prev := -1
	for i := 0; i < 600; i++ {
		m.scrollLines(-5)
		if m.viewport.YOffset() == prev {
			break
		}
		prev = m.viewport.YOffset()
	}

	if m.viewport.YOffset() != 0 {
		t.Fatalf("一直上滑后 YOffset=%d，未到顶", m.viewport.YOffset())
	}
	if !strings.Contains(plainText(m.renderFrame()), "内容 0000") {
		t.Fatal("上滑到顶仍看不到第一块")
	}
}

// TestViewportContentGrowsWithTranscript 断言 viewport 拿到的内容量随会话增长，
// 而不是被固定上限锁死。
func TestViewportContentGrowsWithTranscript(t *testing.T) {
	small := inputTestModel(t)
	for i := 0; i < 50; i++ {
		small.addBlock(fmt.Sprintf("块 %d", i))
	}
	small.refreshViewport()

	large := inputTestModel(t)
	for i := 0; i < 500; i++ {
		large.addBlock(fmt.Sprintf("块 %d", i))
	}
	large.refreshViewport()

	smallLen := len(small.transcriptContent())
	largeLen := len(large.transcriptContent())
	if largeLen <= smallLen*2 {
		t.Fatalf("内容量没有随会话增长: 50 块=%d 字节, 500 块=%d 字节", smallLen, largeLen)
	}
}

// TestAppendCostStaysBounded 断言「追加一块」的成本不随历史线性增长 ——
// 这是当初引入窗口的原因，修复不能把这个性能问题带回来。
func TestAppendCostStaysBounded(t *testing.T) {
	small := inputTestModel(t)
	for i := 0; i < 50; i++ {
		small.addBlock(fmt.Sprintf("块 %d", i))
	}
	small.refreshViewport()
	smallCost := appendCost(small)

	large := inputTestModel(t)
	for i := 0; i < 1000; i++ {
		large.addBlock(fmt.Sprintf("块 %d", i))
	}
	large.refreshViewport()
	largeCost := appendCost(large)

	// 允许一定增长（内容确实更多），但不能是 20 倍那种线性爆炸。
	if largeCost > smallCost*8 {
		t.Fatalf("追加成本随历史爆炸: 50 块=%d 字节, 1000 块=%d 字节", smallCost, largeCost)
	}
}

// appendCost 返回一次追加操作推进 viewport 的字节数，作为「是否全量重建」的代理指标。
func appendCost(m *Model) int {
	before := m.viewportRebuildBytes
	m.addBlock("→ tool output")
	m.refreshViewport()
	return m.viewportRebuildBytes - before
}

// TestScrollBackBoundaries 覆盖边界。
func TestScrollBackBoundaries(t *testing.T) {
	t.Run("空会话滚到顶不 panic", func(t *testing.T) {
		m := inputTestModel(t)
		m.refreshViewport()
		m.viewport.GotoTop()
		_ = m.renderFrame()
	})

	t.Run("单块会话", func(t *testing.T) {
		m := inputTestModel(t)
		m.addBlock("唯一一块")
		m.refreshViewport()
		m.viewport.GotoTop()
		if !strings.Contains(plainText(m.renderFrame()), "唯一一块") {
			t.Fatal("单块会话看不到内容")
		}
	})

	t.Run("清空后回溯到空", func(t *testing.T) {
		m := inputTestModel(t)
		for i := 0; i < 100; i++ {
			m.addBlock(fmt.Sprintf("块 %d", i))
		}
		m.refreshViewport()
		m.blocks = nil
		m.markBlocksChanged()
		m.refreshViewport()
		m.viewport.GotoTop()
		if strings.Contains(plainText(m.renderFrame()), "块 0") {
			t.Fatal("清空后仍能看到旧内容")
		}
	})
}
