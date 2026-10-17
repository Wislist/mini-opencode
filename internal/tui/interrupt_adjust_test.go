package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wislist/mini-opencode/internal/agent"
)

// Interrupt-and-adjust: esc pauses a run instead of discarding what it had
// already produced, the next message continues from that point with the task
// list intact, and a new run is never started while the previous one is still
// unwinding.

// interruptModel is a running model with a runtime and a session, which is what
// the continuation path needs in order to preserve the task list.
func interruptModel(t *testing.T) *Model {
	t.Helper()
	m := todoTestModel(t)
	m.SetSessionStore(newTestSessionStore(t, t.TempDir()))
	m.currentSession = m.sessions.Create("t")
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))
	m.input.Focus()
	m.lastUserGoal = "original goal"
	m.state = stateRunning
	return m
}

// silentProgram installs a program whose context is already cancelled, so the
// runtime goroutine's Send calls become no-ops and tests never block on a real
// UI loop.
func silentProgram(t *testing.T, m *Model) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m.SetProgram(tea.NewProgram(m, tea.WithContext(ctx)))
	cancel()
}

// startStreaming pushes one delta and renders it, leaving the model mid-answer.
func startStreaming(t *testing.T, m *Model, text string) {
	t.Helper()
	if cmd := m.handleRuntimeEvent(agent.Event{Type: agent.EventAssistantDelta, Delta: text}); cmd == nil {
		t.Fatal("delta did not schedule a repaint")
	}
	m.Update(streamRenderMsg{})
}

func TestInterruptPreservesPartialAnswer(t *testing.T) {
	m := interruptModel(t)
	startStreaming(t, m, "half an answer")

	m.Update(key("esc"))

	if m.state != stateIdle {
		t.Fatalf("state = %v, want stateIdle", m.state)
	}
	if !m.paused {
		t.Fatal("esc did not mark the run paused")
	}
	if !m.runUnwinding {
		t.Fatal("esc did not wait for the previous run to unwind")
	}
	if got := m.input.Value(); got != "original goal" {
		t.Fatalf("composer prefill = %q, want the previous goal", got)
	}
	if !strings.Contains(plainText(m.renderHelpBar()), "paused") {
		t.Fatalf("help bar does not announce the pause: %q", plainText(m.renderHelpBar()))
	}

	m.Update(runtimeDoneMsg{err: context.Canceled})

	msgs := m.runtime.Messages()
	if len(msgs) != 1 {
		t.Fatalf("runtime messages = %d, want the interrupted assistant message", len(msgs))
	}
	if msgs[0].Role != agent.RoleAssistant || !strings.Contains(msgs[0].Content, "half an answer") {
		t.Fatalf("interrupted answer not recorded: %+v", msgs[0])
	}
	if m.runUnwinding {
		t.Fatal("runtimeDoneMsg left the model unwinding")
	}
}

func TestInterruptSuppressesCancellationError(t *testing.T) {
	m := interruptModel(t)
	startStreaming(t, m, "partial")

	m.Update(key("esc"))
	m.Update(runtimeDoneMsg{err: context.Canceled})

	joined := plainText(strings.Join(m.blocks, "\n"))
	if strings.Contains(joined, "context canceled") {
		t.Fatalf("interrupt reported the provider cancellation: %q", joined)
	}
}

func TestLateDeltasAreIgnoredAfterInterrupt(t *testing.T) {
	m := interruptModel(t)
	startStreaming(t, m, "partial")
	m.Update(key("esc"))

	before := len(m.blocks)
	if cmd := m.handleRuntimeEvent(agent.Event{Type: agent.EventAssistantDelta, Delta: "late"}); cmd != nil {
		t.Fatal("late delta scheduled a repaint after the interrupt")
	}
	if m.streamingText != "" {
		t.Fatalf("late delta was accumulated: %q", m.streamingText)
	}
	if len(m.blocks) != before {
		t.Fatalf("late delta added a ghost block: %d -> %d", before, len(m.blocks))
	}
}

func TestFullResponseAfterInterruptReplacesPartial(t *testing.T) {
	m := interruptModel(t)
	startStreaming(t, m, "partial")

	m.Update(key("esc"))
	m.Update(runtimeEventMsg{event: agent.Event{
		Type:    agent.EventAssistantResponse,
		Message: &agent.Message{Role: agent.RoleAssistant, Content: "complete answer"},
	}})
	m.Update(runtimeDoneMsg{err: context.Canceled})

	joined := plainText(strings.Join(m.blocks, "\n"))
	if !strings.Contains(joined, "complete answer") {
		t.Fatalf("late complete answer missing: %q", joined)
	}
	if strings.Contains(joined, "partial") {
		t.Fatalf("partial answer was rendered twice: %q", joined)
	}
	if got := len(m.runtime.Messages()); got != 0 {
		t.Fatalf("runtime messages = %d, want no duplicate partial", got)
	}
}

func TestInterruptPrefillDoesNotClobberDraft(t *testing.T) {
	m := interruptModel(t)
	m.input.SetValue("my correction")
	startStreaming(t, m, "partial")

	m.Update(key("esc"))

	if got := m.input.Value(); got != "my correction" {
		t.Fatalf("esc overwrote the draft: %q", got)
	}
}

func TestInterruptWithoutDeltaRecordsNothing(t *testing.T) {
	m := interruptModel(t)

	m.Update(key("esc"))
	m.Update(runtimeDoneMsg{err: context.Canceled})

	if got := len(m.runtime.Messages()); got != 0 {
		t.Fatalf("runtime messages = %d, want 0 without streamed content", got)
	}
	if m.paused != true {
		t.Fatal("interrupt without content still has to pause the run")
	}
}

func TestContinuationKeepsTaskList(t *testing.T) {
	m := interruptModel(t)
	if err := m.sessions.Save(m.currentSession); err != nil {
		t.Fatalf("save session: %v", err)
	}
	setTodos(t, m, "[>] half done task\n[ ] next task\n(0/2 done)")
	if err := m.sessions.SaveTodos(m.currentSession.ID, `[{"content":"half done task","status":"in_progress"}]`); err != nil {
		t.Fatalf("save todos: %v", err)
	}

	// A normal new turn clears the list; the continuation path must not.
	m.startNewTurn("next question")
	if panel := m.renderTodoPanel(); panel != "" {
		t.Fatalf("normal turn kept the list:\n%s", panel)
	}

	m.todos = "[>] half done task\n[ ] next task\n(0/2 done)"
	if err := m.sessions.SaveTodos(m.currentSession.ID, `[{"content":"half done task","status":"in_progress"}]`); err != nil {
		t.Fatalf("save todos: %v", err)
	}
	m.startNewTurnKeepingTodos("continue")

	if panel := m.renderTodoPanel(); panel == "" {
		t.Fatal("continuation cleared the task list")
	}
	if got := m.sessions.LoadTodos(m.currentSession.ID); got == "" || got == "[]" {
		t.Fatalf("continuation cleared the stored task list: %q", got)
	}
}

func TestContinuationSendTextMarksAdjustment(t *testing.T) {
	m := interruptModel(t)

	same := m.continuationSendText("original goal")
	if !strings.Contains(same, "continue") || strings.Contains(same, "goal adjusted") {
		t.Fatalf("unchanged goal was not sent as a continuation: %q", same)
	}

	adjusted := m.continuationSendText("use SQLite instead")
	if !strings.Contains(adjusted, "goal adjusted") || !strings.Contains(adjusted, "use SQLite instead") {
		t.Fatalf("adjusted goal missing its marker or text: %q", adjusted)
	}

	m.mode = ModePlan
	plan := m.continuationSendText("use SQLite instead")
	if !strings.Contains(plan, "plan mode") {
		t.Fatalf("plan-mode continuation lost its read-only note: %q", plan)
	}
}

func TestSendWhileUnwindingIsQueued(t *testing.T) {
	m := interruptModel(t)
	silentProgram(t, m)
	startStreaming(t, m, "partial")
	m.Update(key("esc"))
	m.input.SetValue("adjusted goal")

	model, _ := m.Update(key("enter"))
	m = model.(*Model)

	if m.state != stateIdle {
		t.Fatalf("new run started while the previous one was unwinding: state = %v", m.state)
	}
	if m.pendingInput != "adjusted goal" {
		t.Fatalf("pending input = %q, want the queued message", m.pendingInput)
	}

	m.Update(runtimeDoneMsg{err: context.Canceled})
	model, _ = m.Update(flushPendingInputMsg{})
	m = model.(*Model)

	if m.pendingInput != "" {
		t.Fatalf("pending input was not flushed: %q", m.pendingInput)
	}
	if m.state != stateRunning {
		t.Fatalf("queued message did not start the continuation: state = %v", m.state)
	}
	if m.paused {
		t.Fatal("continuation left the model paused")
	}
}

func TestRepeatedEscIsIdempotent(t *testing.T) {
	m := interruptModel(t)
	startStreaming(t, m, "partial")

	m.Update(key("esc"))
	blocks, msgs := len(m.blocks), len(m.runtime.Messages())
	m.Update(key("esc"))

	if len(m.blocks) != blocks {
		t.Fatalf("second esc added blocks: %d -> %d", blocks, len(m.blocks))
	}
	if len(m.runtime.Messages()) != msgs {
		t.Fatal("second esc appended runtime messages")
	}
	if !m.paused {
		t.Fatal("second esc cleared the paused state")
	}
}

// TestEscDuringPermissionDeniesBeforePausing keeps the two meanings of esc in
// order: the pending approval is answered first, and a second esc interrupts.
func TestEscDuringPermissionDeniesBeforePausing(t *testing.T) {
	m := interruptModel(t)
	resp := make(chan permissionDecision, 1)
	m.pendingPerm = &permissionRequestMsg{call: agent.ToolCall{Name: "bash"}, resp: resp}
	m.state = statePermission

	m.Update(key("esc"))
	if m.state != stateRunning || m.paused {
		t.Fatalf("first esc did not just deny the tool: state=%v paused=%v", m.state, m.paused)
	}
	select {
	case decision := <-resp:
		if decision.allow {
			t.Fatal("esc approved the tool call")
		}
	default:
		t.Fatal("esc did not answer the permission request")
	}

	m.Update(key("esc"))
	if !m.paused {
		t.Fatal("second esc did not pause the run")
	}
}

// TestPausedHelpBarFitsTerminalWidth keeps the pause hint inside the frame on
// narrow terminals, like every other help-bar state.
func TestPausedHelpBarFitsTerminalWidth(t *testing.T) {
	for _, w := range []int{120, 80, 40, 20, 8, 1} {
		m := interruptModel(t)
		m.width = w
		m.paused = true
		if got := lipgloss.Width(m.renderHelpBar()); got > w {
			t.Fatalf("w=%d: paused help bar width %d overflows", w, got)
		}
	}
}

// TestInterruptPrefillRecoversLoadedSessionGoal covers a resumed session, which
// has no send-time goal recorded.
func TestInterruptPrefillRecoversLoadedSessionGoal(t *testing.T) {
	m := interruptModel(t)
	m.lastUserGoal = ""
	m.runtime.SetMessages([]agent.Message{
		{Role: agent.RoleUser, Content: "You are in plan mode. Only plan.\n\nthe real goal"},
	})
	m.state = stateRunning

	m.Update(key("esc"))

	if got := m.input.Value(); got != "the real goal" {
		t.Fatalf("prefill = %q, want the unwrapped goal", got)
	}
}
