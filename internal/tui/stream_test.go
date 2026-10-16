package tui

import (
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

func streamModel(t *testing.T) *Model {
	t.Helper()
	cfg := config.Config{User: "u", Assistant: "a"}
	m := New(&cfg, t.TempDir(), "test")
	m.width = 100
	m.height = 40
	m.state = stateRunning
	return m
}

// TestStreamingDeltasRenderOnTick is the regression test for the stall that
// motivated throttling: accumulating text must schedule a repaint, and that
// repaint must actually put the text on screen.
//
// It also covers the bug where the scheduling command was dropped, which left
// the transcript empty forever because the timer never fired.
func TestStreamingDeltasRenderOnTick(t *testing.T) {
	m := streamModel(t)

	cmd := m.handleRuntimeEvent(agent.Event{
		Type:  agent.EventAssistantDelta,
		Delta: "hello ",
	})
	if cmd == nil {
		t.Fatal("first delta did not schedule a repaint")
	}
	// Accumulation alone must not have rendered anything yet.
	if m.streamingIdx >= 0 {
		t.Fatal("delta rendered synchronously; throttling is not in effect")
	}
	if m.streamingText != "hello " {
		t.Fatalf("streamingText = %q", m.streamingText)
	}

	// A second delta while a repaint is pending must not queue another one.
	if extra := m.handleRuntimeEvent(agent.Event{Type: agent.EventAssistantDelta, Delta: "world"}); extra != nil {
		t.Fatal("second delta queued a redundant repaint")
	}

	// Deliver the timer message, which is what actually renders.
	m.Update(streamRenderMsg{})

	if m.streamingIdx < 0 {
		t.Fatal("tick did not create the streaming block")
	}
	if got := m.blocks[m.streamingIdx]; !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Fatalf("streamed block missing text: %q", got)
	}
}

// TestStreamRenderCoalescesManyDeltas asserts that a burst of deltas produces
// one scheduled render rather than one per delta, which is the whole point of
// the throttle.
func TestStreamRenderCoalescesManyDeltas(t *testing.T) {
	m := streamModel(t)

	scheduled := 0
	for i := 0; i < 200; i++ {
		if cmd := m.handleRuntimeEvent(agent.Event{Type: agent.EventAssistantDelta, Delta: "x"}); cmd != nil {
			scheduled++
			// Simulate the tick not having arrived yet: the pending flag stays
			// set, so subsequent deltas must not reschedule.
		}
	}
	if scheduled != 1 {
		t.Fatalf("scheduled %d renders for 200 deltas, want 1", scheduled)
	}
	if len(m.streamingText) != 200 {
		t.Fatalf("streamingText length = %d, want 200", len(m.streamingText))
	}
}

// TestFinalResponseCancelsPendingRender guards the race where a queued tick
// fires after the authoritative response and overwrites it with a partial
// render.
func TestFinalResponseCancelsPendingRender(t *testing.T) {
	m := streamModel(t)

	m.handleRuntimeEvent(agent.Event{Type: agent.EventAssistantDelta, Delta: "partial"})
	if !m.streamRenderPending {
		t.Fatal("no render pending before the final response")
	}

	m.handleRuntimeEvent(agent.Event{
		Type:    agent.EventAssistantResponse,
		Message: &agent.Message{Role: agent.RoleAssistant, Content: "the final answer"},
	})
	if m.streamRenderPending {
		t.Fatal("final response left a throttled render pending")
	}

	// A late tick must be a no-op rather than repainting stale text.
	m.Update(streamRenderMsg{})
	// Compare on plain text: the Markdown renderer interleaves ANSI escapes
	// between words, so a raw substring check would be unreliable.
	joined := plainText(strings.Join(m.blocks, "\n"))
	if !strings.Contains(joined, "the final answer") {
		t.Fatalf("final content missing: %q", joined)
	}
	if strings.Contains(joined, "partial") {
		t.Fatalf("late tick repainted stale text: %q", joined)
	}
	if m.streamingText != "" || m.streamingIdx != -1 {
		t.Fatalf("streaming state not reset: text=%q idx=%d", m.streamingText, m.streamingIdx)
	}
}

// plainText strips ANSI escape sequences so assertions can match the visible
// characters rather than the styled output.
func plainText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestStreamRenderTickWithoutContentIsSafe covers the first tick arriving
// before any delta, and a tick after the stream already ended.
func TestStreamRenderTickWithoutContentIsSafe(t *testing.T) {
	m := streamModel(t)
	before := len(m.blocks)
	m.Update(streamRenderMsg{})
	if len(m.blocks) != before {
		t.Fatalf("empty tick added a block: %d -> %d", before, len(m.blocks))
	}
	if m.streamRenderPending {
		t.Fatal("tick left the pending flag set")
	}
}

// TestSpinnerKeepsTickingWhileRunning documents that the spinner still receives
// its ticks, since a starved spinner was the visible symptom of the stall.
func TestSpinnerKeepsTickingWhileRunning(t *testing.T) {
	m := streamModel(t)
	m.handleRuntimeEvent(agent.Event{Type: agent.EventAssistantDelta, Delta: "more"})

	// A throttled render must not stop the spinner from rescheduling itself.
	msg := m.spinner.Tick()
	if _, cmd := m.Update(msg); cmd == nil {
		t.Fatal("spinner tick did not reschedule itself while running")
	}
}

// TestSpinnerKeepsAnimatingWithCachedFooter guards the visible freeze where
// the spinner model advanced but renderFooter kept returning its cached first
// frame. Consecutive real ticks must change the rendered help bar without
// requiring any unrelated input to invalidate the footer.
func TestSpinnerKeepsAnimatingWithCachedFooter(t *testing.T) {
	m := streamModel(t)
	first := m.renderFooter()[len(m.renderFooter())-1]

	_, next := m.Update(m.spinner.Tick())
	if next == nil {
		t.Fatal("first spinner tick did not schedule the next frame")
	}
	second := m.renderFooter()[len(m.renderFooter())-1]
	if second == first {
		t.Fatal("cached footer kept showing the first spinner frame")
	}

	msg := next()
	_, next = m.Update(msg)
	if next == nil {
		t.Fatal("second spinner tick did not schedule the next frame")
	}
	third := m.renderFooter()[len(m.renderFooter())-1]
	if third == second {
		t.Fatal("spinner stopped changing after the first visible frame")
	}
}

// TestSpinnerKeepsTickingBehindRunOverlays covers temporary UI states that
// pause interaction without ending the active runtime. A tick arriving while
// permission or plan approval is visible must still schedule the next tick;
// otherwise the animation is permanently stopped when the overlay closes.
func TestSpinnerKeepsTickingBehindRunOverlays(t *testing.T) {
	for _, state := range []appState{statePermission, statePlanApproval} {
		m := streamModel(t)
		m.todos = "[>] run checks"
		first := m.spinner.View()
		m.state = state

		_, next := m.Update(m.spinner.Tick())
		if next == nil {
			t.Fatalf("state %v stopped the spinner tick chain", state)
		}
		if got := m.spinner.View(); got == first {
			t.Fatalf("state %v did not advance the spinner frame", state)
		}
	}
}

func TestSpinnerKeepsAnimatingAfterTodoPanelAppears(t *testing.T) {
	m := streamModel(t)
	m.Update(runtimeEventMsg{event: agent.Event{
		Type:  agent.EventTodosChanged,
		Todos: "[>] run checks",
	}})
	footer := m.renderFooter()
	first := footer[len(footer)-1]

	_, next := m.Update(m.spinner.Tick())
	if next == nil {
		t.Fatal("todo update stopped the spinner tick chain")
	}
	footer = m.renderFooter()
	if got := footer[len(footer)-1]; got == first {
		t.Fatal("spinner frame did not change after the todo panel appeared")
	}
}
