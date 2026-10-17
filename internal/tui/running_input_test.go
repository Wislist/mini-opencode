package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

// The prompt must stay editable while a run is streaming. Drafting the next
// message is a normal thing to do while the agent works; dropping the keys is
// not.

func TestRunningPromptAcceptsTyping(t *testing.T) {
	m := streamModel(t)
	m.input.Focus()

	m = typeText(m, "follow-up question")

	if got := m.input.Value(); got != "follow-up question" {
		t.Fatalf("运行中输入被丢弃: got %q", got)
	}
}

// TestRunningPromptAcceptsChineseInput covers the reported swallowed-character
// bug: Chinese (and any other non-ASCII text) arrives as runes with Text set,
// and the running-state key handler used to return without forwarding them.
func TestRunningPromptAcceptsChineseInput(t *testing.T) {
	m := streamModel(t)
	m.input.Focus()

	m = typeText(m, "修改回答")

	if got := m.input.Value(); got != "修改回答" {
		t.Fatalf("运行中中文输入吞字: got %q", got)
	}
}

// TestRunningPromptAcceptsIMECommitText guards a commit that carries more than
// one rune in a single key message, which some input methods produce.
func TestRunningPromptAcceptsIMECommitText(t *testing.T) {
	m := streamModel(t)
	m.input.Focus()

	updated, _ := m.Update(tea.KeyPressMsg{Code: '中', Text: "中文"})
	m = updated.(*Model)

	if got := m.input.Value(); got != "中文" {
		t.Fatalf("IME 提交多字符时吞字: got %q", got)
	}
}

// TestStartRunKeepsPromptFocus is the companion fix: a blurred textarea drops
// every key regardless of how the handler forwards it.
func TestStartRunKeepsPromptFocus(t *testing.T) {
	m := inputTestModel(t)
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))

	// A cancelled program context makes Program.Send a no-op, so the runtime
	// goroutine cannot block the test.
	ctx, cancel := context.WithCancel(context.Background())
	m.SetProgram(tea.NewProgram(m, tea.WithContext(ctx)))
	cancel()

	m.input.Blur()
	if m.input.Focused() {
		t.Fatal("test setup failed to blur the prompt")
	}

	updated, _ := m.startRun("hello")
	got := updated.(*Model)
	if got.cancel != nil {
		got.cancel()
	}

	if !got.input.Focused() {
		t.Fatal("startRun blurred the prompt, so typing during the run is impossible")
	}
	if got.state != stateRunning {
		t.Fatalf("state = %v, want stateRunning", got.state)
	}
}

// TestEnterWhileRunningKeepsDraft ensures the draft is not sent or turned into
// a newline while another turn is still owning the runtime.
func TestEnterWhileRunningKeepsDraft(t *testing.T) {
	m := streamModel(t)
	m.input.Focus()
	m.input.SetValue("draft")

	m = pressKey(m, key("enter"))

	if m.state != stateRunning {
		t.Fatalf("Enter started a new turn while running: state = %v", m.state)
	}
	if got := m.input.Value(); got != "draft" {
		t.Fatalf("Enter changed the running draft: got %q", got)
	}
}

// TestStreamingRepaintKeepsDraft proves transcript updates and the composer are
// independent: a throttled repaint and the final response must not clear or
// reorder what the user typed.
func TestStreamingRepaintKeepsDraft(t *testing.T) {
	m := streamModel(t)
	m.input.Focus()
	m = typeText(m, "next")

	cmd := m.handleRuntimeEvent(agent.Event{Type: agent.EventAssistantDelta, Delta: "partial answer"})
	if cmd == nil {
		t.Fatal("delta did not schedule a repaint")
	}
	m.Update(streamRenderMsg{})
	m.handleRuntimeEvent(agent.Event{
		Type:    agent.EventAssistantResponse,
		Message: &agent.Message{Role: agent.RoleAssistant, Content: "final answer"},
	})

	if got := m.input.Value(); got != "next" {
		t.Fatalf("streaming repaint clobbered the draft: got %q", got)
	}
}

// TestTypingDoesNotFlushStreamingRender is the performance guard: keystrokes
// must only edit the composer. The transcript keeps using the throttled repaint
// path, so typing during a fast stream cannot force an extra Markdown render.
func TestTypingDoesNotFlushStreamingRender(t *testing.T) {
	m := streamModel(t)
	m.input.Focus()

	if cmd := m.handleRuntimeEvent(agent.Event{Type: agent.EventAssistantDelta, Delta: "streaming"}); cmd == nil {
		t.Fatal("delta did not schedule a repaint")
	}

	m = typeText(m, "draft")

	if !m.streamRenderPending {
		t.Fatal("typing cleared the pending throttled repaint")
	}
	if m.streamingIdx >= 0 {
		t.Fatal("typing rendered the streaming block synchronously")
	}
	if got := m.input.Value(); got != "draft" {
		t.Fatalf("draft was not kept: %q", got)
	}
}

// TestNativeCursorTracksPromptWhileRunning keeps the real terminal cursor in
// the composer so IME pre-edit text appears there instead of on the help bar.
func TestNativeCursorTracksPromptWhileRunning(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(*Model)
	m.state = stateRunning
	m.input.Focus()
	m.input.SetValue("draft")
	m.input.CursorEnd()

	view := m.renderFrame()

	col, row, ok := m.NativeCursorPosition()
	if !ok {
		t.Fatal("NativeCursorPosition ok = false while running")
	}
	if col != 10 {
		t.Fatalf("col = %d, want 10", col)
	}
	lines := strings.Split(view, "\n")
	if row > len(lines) {
		t.Fatalf("cursor row %d outside view of %d lines", row, len(lines))
	}
	if inputRow := lines[row-1]; !strings.Contains(inputRow, "draft") {
		t.Fatalf("cursor row %q does not contain the draft", inputRow)
	}
}

// TestRunningFrameKeepsDraftAndStreamSeparate covers the layout concern: the
// streamed answer stays in the transcript and the draft stays in the composer,
// with the cursor on the composer's last line rather than in the transcript.
func TestRunningFrameKeepsDraftAndStreamSeparate(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(*Model)
	m.state = stateRunning
	m.input.Focus()
	m.input.SetValue("first line\n我的草稿")
	m.input.CursorEnd()
	m.addBlock("streamed answer line")
	m.refreshViewport()

	view := m.renderFrame()
	plainLines := strings.Split(plainText(view), "\n")
	if len(plainLines) > m.height {
		t.Fatalf("frame has %d lines, terminal height %d", len(plainLines), m.height)
	}

	streamRow, draftRow := -1, -1
	for i, line := range plainLines {
		if strings.Contains(line, "streamed answer line") {
			streamRow = i
		}
		if strings.Contains(line, "我的草稿") {
			draftRow = i
		}
	}
	if streamRow < 0 {
		t.Fatal("streamed answer missing from the frame")
	}
	if draftRow < 0 {
		t.Fatal("draft missing from the frame")
	}
	if draftRow <= streamRow {
		t.Fatalf("draft row %d overlaps or precedes streamed answer row %d", draftRow, streamRow)
	}

	_, row, ok := m.NativeCursorPosition()
	if !ok {
		t.Fatal("NativeCursorPosition ok = false while running")
	}
	if row-1 != draftRow {
		t.Fatalf("cursor row = %d, want draft row %d", row-1, draftRow)
	}
}

// TestNativeCursorUsesDisplayWidthForCJK keeps the IME cursor aligned with
// double-width characters: counting runes would place it two columns early.
func TestNativeCursorUsesDisplayWidthForCJK(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = model.(*Model)
	m.state = stateRunning
	m.input.Focus()
	m.input.SetValue("中文")
	m.input.CursorEnd()

	_ = m.renderFrame()

	col, _, ok := m.NativeCursorPosition()
	if !ok {
		t.Fatal("NativeCursorPosition ok = false while running")
	}
	// border + padding + "❯ " (4) + two double-width runes (4), 1-based.
	if col != 9 {
		t.Fatalf("col = %d, want 9", col)
	}
}
