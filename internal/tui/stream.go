package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// streamRenderInterval is the minimum gap between Markdown renders of the block
// currently being streamed.
//
// Rendering one assistant message runs Glamour over the whole accumulated text,
// which measures at ~3.4ms for 4KB and ~12.8ms for 16KB on an M2. A provider
// emits deltas far faster than that (often many per millisecond), so rendering
// every delta pushed a frame well past the 16.6ms budget at 60fps: the
// transcript visibly stuttered and the spinner, which shares the same update
// loop, was starved of its ticks and appeared to freeze. Coalescing deltas and
// rendering on an interval keeps the display responsive without changing what
// is ultimately shown — the final response is rendered from the authoritative
// content, independent of this timer.
const streamRenderInterval = 33 * time.Millisecond

// streamRenderMsg asks for a throttled repaint of the streaming block.
type streamRenderMsg struct{}

// scheduleStreamRender queues a repaint unless one is already pending, so a
// burst of deltas collapses into a single render.
func (m *Model) scheduleStreamRender() tea.Cmd {
	if m.streamRenderPending {
		return nil
	}
	m.streamRenderPending = true
	return tea.Tick(streamRenderInterval, func(time.Time) tea.Msg {
		return streamRenderMsg{}
	})
}

// cancelStreamRender drops a pending throttled repaint. The final response uses
// this so a late timer cannot overwrite the authoritative render with a partial
// one.
func (m *Model) cancelStreamRender() {
	m.streamRenderPending = false
}

// flushStreamRender repaints the streaming block if there is content waiting.
// It is the timer's handler and does the actual rendering work.
func (m *Model) flushStreamRender() {
	m.streamRenderPending = false
	if m.streamingText == "" {
		return
	}
	rendered := m.renderStreamingMessage(m.streamingText)
	if m.streamingIdx < 0 {
		m.addBlock(rendered)
		m.streamingIdx = len(m.blocks) - 1
	} else {
		m.blocks[m.streamingIdx] = rendered
		m.markBlocksChanged()
	}
	m.refreshViewport()
}
