package tui

import (
	"testing"
)

// Auto-follow across turns.
//
// The viewport follows new content only while the user is at the bottom, so the
// "was the user at the bottom?" question must be answered against the frame the
// user was actually looking at. Answering it after the newly appended block had
// already been pushed in made every send look like the user had scrolled up,
// and the answer then streamed off screen for the whole turn.

// TestRenderAfterAppendKeepsFollowing reproduces the ordering bug directly: an
// append that is synced during rendering must not be mistaken for the user
// having scrolled away.
func TestRenderAfterAppendKeepsFollowing(t *testing.T) {
	m := perfTestModel(t, 100, 24)
	fillTranscript(m, 200)
	m.viewport.GotoBottom()
	_ = m.renderFrame()

	m.addBlock("user question")
	_ = m.renderFrame()

	if !m.viewport.AtBottom() {
		t.Fatalf("append during render lost the bottom: y=%d", m.viewport.YOffset())
	}
}

// TestStreamingFollowsAfterSend is the reported symptom: after one answer has
// completed, sending the next question must make the new answer track the
// bottom instead of staying frozen where the previous turn left it.
func TestStreamingFollowsAfterSend(t *testing.T) {
	m := perfTestModel(t, 100, 24)
	fillTranscript(m, 200)
	m.viewport.GotoBottom()
	_ = m.renderFrame()

	// The draft makes the prompt box taller, then sending clears it and appends
	// the user message: footer height and content change in the same frame.
	m.input.SetValue("a draft long enough to wrap inside a narrow prompt box")
	m.syncInputHeight()
	_ = m.renderFrame()

	m.input.Reset()
	m.syncInputHeight()
	m.addBlock("user question")
	_ = m.renderFrame()

	if !m.viewport.AtBottom() {
		t.Fatalf("after send, viewport is not at the bottom: y=%d", m.viewport.YOffset())
	}

	m.streamingText = "answer"
	m.streamingIdx = -1
	m.flushStreamRender()
	for i := 0; i < 10; i++ {
		m.streamingText += " more"
		m.flushStreamRender()
		if !m.viewport.AtBottom() {
			t.Fatalf("streaming stopped following at delta %d: y=%d", i, m.viewport.YOffset())
		}
	}
}

// TestSendingJumpsToLatestFromScrolledUp covers the user-initiated jump: after
// reading history, sending a message returns to the newest content because that
// is what sending asks for.
func TestSendingJumpsToLatestFromScrolledUp(t *testing.T) {
	m := perfTestModel(t, 100, 24)
	fillTranscript(m, 200)
	m.viewport.GotoBottom()
	_ = m.renderFrame()

	m.scrollLines(-40)
	if m.viewport.AtBottom() {
		t.Fatal("test setup failed to scroll up")
	}

	m.addBlock("user question")
	m.scrollToLatest()

	if !m.viewport.AtBottom() {
		t.Fatalf("sending did not return to the newest content: y=%d", m.viewport.YOffset())
	}
}

// TestScrolledUpStillStaysPutDuringStreaming guards the earlier fix: the
// user-initiated jump on send must not turn into unconditional following while
// the answer streams.
func TestScrolledUpStillStaysPutDuringStreaming(t *testing.T) {
	m := perfTestModel(t, 100, 24)
	fillTranscript(m, 200)
	m.viewport.GotoBottom()
	_ = m.renderFrame()

	m.scrollLines(-40)
	before := m.viewport.YOffset()

	for i := 0; i < 5; i++ {
		m.appendStreamingDelta("more streamed text ")
		m.flushStreamRender()
	}

	if m.viewport.AtBottom() {
		t.Fatal("streaming yanked the view back to the bottom")
	}
	if m.viewport.YOffset() != before {
		t.Fatalf("scroll position changed while streaming: %d -> %d", before, m.viewport.YOffset())
	}
}
