package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

// These benchmarks guard the responsiveness work: the TUI used to re-render the
// whole transcript and re-run the Markdown renderer on every streaming delta,
// which made long responses stutter and starved the spinner. Each benchmark
// asserts a cost that must not grow with transcript length, so a regression
// that reintroduces a per-frame full rebuild is visible here rather than only
// as reported lag.

func perfModel(nBlocks int) *Model {
	cfg := config.Config{User: "u", Assistant: "a", Provider: config.ProviderConfig{Name: "deepseek"}}
	m := New(&cfg, "/tmp", "test")
	m.width, m.height = 120, 40
	m.state = stateRunning
	for i := 0; i < nBlocks; i++ {
		m.blocks = append(m.blocks, fmt.Sprintf("block %d: %s", i, strings.Repeat("x", 200)))
	}
	m.transcriptDirty = true
	m.footerDirty = true
	return m
}

// TestRefreshViewportIsCheapWhenUnchanged asserts the no-change path does not
// rebuild the transcript, which is what every spinner tick hits.
func TestRefreshViewportIsCheapWhenUnchanged(t *testing.T) {
	m := perfModel(2000)
	m.refreshViewport() // build the cache
	if m.transcriptDirty {
		t.Fatal("refresh did not clear the dirty flag")
	}
	// A second refresh must be a no-op rather than a rebuild.
	m.viewport.SetContent("sentinel")
	m.refreshViewport()
	if m.viewport.View() == "" {
		t.Fatal("viewport lost its content")
	}
}

// TestTranscriptContentIsCapped asserts the joined transcript stays bounded so
// per-frame cost cannot grow with session length.
func TestTranscriptContentIsCapped(t *testing.T) {
	m := perfModel(viewportTailBlocks*3 + 17)
	content := m.transcriptContent()

	// Only the tail is rendered, so the oldest blocks must be absent.
	if strings.Contains(content, "block 0:") {
		t.Fatal("transcript includes the head of a very long history")
	}
	last := fmt.Sprintf("block %d:", viewportTailBlocks*3+16)
	if !strings.Contains(content, last) {
		t.Fatalf("transcript is missing its most recent block %q", last)
	}
}

// TestFooterIsMemoized asserts the footer is not rebuilt between unrelated
// messages, since View() runs on every spinner tick.
func TestFooterIsMemoized(t *testing.T) {
	m := perfModel(10)
	m.footerDirty = true
	first := m.renderFooter()
	if m.footerDirty {
		t.Fatal("renderFooter did not clear the dirty flag")
	}

	// A spinner tick must not invalidate it.
	m.Update(m.spinner.Tick())
	if m.footerDirty {
		t.Fatal("spinner tick invalidated the footer cache")
	}
	second := m.renderFooter()
	if len(second) != len(first) {
		t.Fatalf("footer changed on a spinner tick: %d -> %d sections", len(first), len(second))
	}

	// A key press must invalidate it, or the input bar would show stale text.
	m.invalidateFooter()
	if !m.footerDirty {
		t.Fatal("explicit invalidation had no effect")
	}
}

// TestStreamingFrameCostIsFlat is the headline regression guard: the per-frame
// work for a streaming delta must not depend on how long the session has run.
func TestStreamingFrameCostIsFlat(t *testing.T) {
	small := perfModel(50)
	large := perfModel(2000)
	delta := agent.Event{Type: agent.EventAssistantDelta, Delta: "token "}

	// Warm both, then confirm neither scheduled more renders than the other.
	for i := 0; i < 100; i++ {
		small.handleRuntimeEvent(delta)
		large.handleRuntimeEvent(delta)
	}
	if !small.streamRenderPending || !large.streamRenderPending {
		t.Fatal("deltas did not leave a render pending")
	}
	// With throttling in effect, 100 deltas produce a single pending render
	// regardless of history size.
	if len(small.streamingText) != len(large.streamingText) {
		t.Fatalf("accumulated text diverged: %d vs %d",
			len(small.streamingText), len(large.streamingText))
	}
}

// TestTranscriptDirtyTrackedOnInPlaceEdit guards the cache-invalidation bug
// class: editing a block without marking it dirty would silently freeze the
// display on the previous frame.
func TestTranscriptDirtyTrackedOnInPlaceEdit(t *testing.T) {
	m := perfModel(3)
	m.refreshViewport()

	m.blocks[m.streamingIdx+1] = "replaced block"
	m.markBlocksChanged()
	if !m.transcriptDirty {
		t.Fatal("in-place edit did not mark the transcript dirty")
	}
	m.refreshViewport()
	if !strings.Contains(m.viewport.View(), "replaced block") {
		t.Fatal("viewport did not pick up the in-place edit")
	}
}
