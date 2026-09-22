package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// memTodoReader is a mutable todo list for chain tests.
type memTodoReader struct {
	mu    sync.Mutex
	items []TodoItem
}

func (r *memTodoReader) Load() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, _ := json.Marshal(r.items)
	return string(data)
}

func (r *memTodoReader) set(items ...TodoItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = items
}

func todo(content, status string) TodoItem { return TodoItem{Content: content, Status: status} }

// completeTodosTool marks an item completed, like the real todo_write would.
type completeTodosTool struct {
	reader *memTodoReader
	// completions counts how many times the agent "did" work.
	completions int
}

func (t *completeTodosTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "advance_todos",
		Description: "test tool: complete the current item",
		InputSchema: map[string]any{"type": "object"},
	}
}

func (t *completeTodosTool) Run(_ context.Context, _ ToolInput) (ToolOutput, error) {
	t.completions++
	items := ParseTodos(t.reader.Load())
	for i := range items {
		if NormalizeTodoStatus(items[i].Status) != TodoCompleted {
			items[i].Status = TodoCompleted
			break
		}
	}
	t.reader.set(items...)
	return ToolOutput{Content: RenderTodos(items), Metadata: map[string]any{
		"todos":    t.reader.Load(),
		"rendered": RenderTodos(items),
	}}, nil
}

// The run keeps going through the task list on its own: no user message between
// items, and it ends once every item is completed.
func TestRuntimeDrivesTodoChainToCompletion(t *testing.T) {
	reader := &memTodoReader{}
	reader.set(todo("first", TodoInProgress), todo("second", TodoPending), todo("third", TodoPending))
	advance := &completeTodosTool{reader: reader}

	// Each round: the model calls the tool, then answers with text only. The
	// loop must re-enter until the list is done.
	var responses []AssistantResponse
	for i := 0; i < 4; i++ {
		call := toolCallResponse("advance_todos")
		call.ToolCalls[0].ID = fmt.Sprintf("call-%d", i)
		responses = append(responses, call, AssistantResponse{Content: fmt.Sprintf("item %d done", i)})
	}
	provider := &scriptedProvider{responses: responses}

	rt := NewRuntime(provider, WithTool(advance), WithTodoReader(reader), WithMaxTurns(20))

	var continuations, finished int
	var injected []string
	err := rt.Run(context.Background(), "do the list", func(event Event) {
		switch event.Type {
		case EventTodoContinuation:
			continuations++
			injected = append(injected, event.Plan)
		case EventRunFinished:
			finished++
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if advance.completions != 3 {
		t.Fatalf("completions = %d, want 3 (one per item)", advance.completions)
	}
	if continuations != 2 {
		t.Fatalf("continuations = %d, want 2 (after item 1 and item 2)", continuations)
	}
	if finished != 1 {
		t.Fatalf("run finished %d times, want exactly 1", finished)
	}
	// The injected message must name the next item and carry the live list.
	if !strings.Contains(injected[0], "second") || !strings.Contains(injected[0], "[x] first") {
		t.Fatalf("first continuation = %q", injected[0])
	}
}

// A blocker reported through todo_blocked ends the chain cleanly: the run
// finishes without an error and the reason reaches the UI.
func TestRuntimeStopsTodoChainOnReportedBlocker(t *testing.T) {
	reader := &memTodoReader{}
	reader.set(todo("needs a decision", TodoPending))

	blocked := metadataTool{
		name:     "todo_blocked",
		readOnly: true,
		metadata: map[string]any{
			"blocked": true,
			"reason":  "the API key is missing",
			"needs":   "a DEEPSEEK_API_KEY",
		},
	}
	provider := &scriptedProvider{responses: []AssistantResponse{
		toolCallResponse("todo_blocked"),
		{Content: "I need the key before continuing"},
	}}
	rt := NewRuntime(provider, WithTool(blocked), WithTodoReader(reader), WithMaxTurns(10))

	var blockReason, blockNeeds string
	blockedEvents := 0
	err := rt.Run(context.Background(), "do the list", func(event Event) {
		if event.Type == EventTodoBlocked {
			blockedEvents++
			blockReason = event.BlockedReason
			blockNeeds = event.BlockedNeeds
		}
		if event.Type == EventTodoContinuation {
			t.Fatal("the chain continued despite a reported blocker")
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want a clean stop", err)
	}
	if blockedEvents != 1 {
		t.Fatalf("blocked events = %d, want 1", blockedEvents)
	}
	if blockReason != "the API key is missing" || blockNeeds != "a DEEPSEEK_API_KEY" {
		t.Fatalf("blocker = %q / %q", blockReason, blockNeeds)
	}
}

// A model that neither advances nor reports a blocker must not spin forever:
// the stall guard ends the chain and says why.
func TestRuntimeTodoChainStopsWhenNothingAdvances(t *testing.T) {
	reader := &memTodoReader{}
	reader.set(todo("stuck item", TodoPending))

	// The model keeps replying with text only and never touches the list.
	provider := &scriptedProvider{responses: []AssistantResponse{
		{Content: "thinking about it"}, {Content: "still thinking"}, {Content: "hmm"},
		{Content: "hmm"}, {Content: "hmm"}, {Content: "hmm"},
	}}
	rt := NewRuntime(provider, WithTodoReader(reader), WithMaxTurns(50))

	var continuations, stops int
	err := rt.Run(context.Background(), "do the list", func(event Event) {
		switch event.Type {
		case EventTodoContinuation:
			continuations++
		case EventTodoChainStopped:
			stops++
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if continuations != maxStalledContinuations {
		t.Fatalf("continuations = %d, want %d", continuations, maxStalledContinuations)
	}
	if stops != 1 {
		t.Fatalf("chain stops = %d, want 1", stops)
	}
}

// Without a todo reader the run stays single-shot, exactly as before.
func TestRuntimeWithoutTodoReaderFinishesAfterOneTurn(t *testing.T) {
	reader := &memTodoReader{}
	reader.set(todo("never attempted", TodoPending))
	provider := &scriptedProvider{responses: []AssistantResponse{{Content: "done talking"}}}

	rt := NewRuntime(provider, WithMaxTurns(10))
	var continuations int
	if err := rt.Run(context.Background(), "hi", func(event Event) {
		if event.Type == EventTodoContinuation {
			continuations++
		}
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if continuations != 0 {
		t.Fatalf("continuations = %d, want 0 without a todo reader", continuations)
	}
}

// noSleep makes run-level retries immediate in tests.
func noSleep(context.Context, time.Duration) error { return nil }

// failingProvider fails the first n completions, then succeeds.
type failingProvider struct {
	failures  int
	attempts  int
	success   AssistantResponse
	responses []AssistantResponse
}

func (p *failingProvider) Complete(context.Context, Request) (AssistantResponse, error) {
	return p.CompleteStream(context.Background(), Request{}, nil)
}

func (p *failingProvider) CompleteStream(context.Context, Request, func(string)) (AssistantResponse, error) {
	p.attempts++
	if p.attempts <= p.failures {
		return AssistantResponse{}, fmt.Errorf("stream interrupted (attempt %d)", p.attempts)
	}
	if len(p.responses) > 0 {
		resp := p.responses[0]
		p.responses = p.responses[1:]
		return resp, nil
	}
	return p.success, nil
}

// A provider failure retries the turn with the transcript intact: no tool runs
// twice, and the run succeeds once a retry lands.
func TestRuntimeRetriesFailedTurn(t *testing.T) {
	advance := &completeTodosTool{reader: &memTodoReader{}}
	provider := &failingProvider{failures: 2, success: AssistantResponse{Content: "recovered"}}

	rt := NewRuntime(provider, WithTool(advance), WithRunRetries(3), WithMaxTurns(5))
	rt.retrySleep = noSleep

	var retries []int
	err := rt.Run(context.Background(), "hi", func(event Event) {
		if event.Type == EventRunRetry {
			retries = append(retries, event.Attempt)
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want recovery", err)
	}
	if len(retries) != 2 || retries[0] != 1 || retries[1] != 2 {
		t.Fatalf("retry attempts = %v, want [1 2]", retries)
	}
	if provider.attempts != 3 {
		t.Fatalf("provider attempts = %d, want 3", provider.attempts)
	}
	// The retried turns added exactly one user message and one assistant
	// message: a failed attempt records nothing.
	messages := rt.Messages()
	if len(messages) != 2 {
		t.Fatalf("messages = %d (%#v), want the user turn plus one reply", len(messages), messages)
	}
	if advance.completions != 0 {
		t.Fatalf("a tool ran %d times during retries, want 0", advance.completions)
	}
}

// Retries are bounded: once they are exhausted the run fails.
func TestRuntimeRetriesAreBounded(t *testing.T) {
	provider := &failingProvider{failures: 99}
	rt := NewRuntime(provider, WithRunRetries(1), WithMaxTurns(5))
	rt.retrySleep = noSleep

	err := rt.Run(context.Background(), "hi", nil)
	if err == nil {
		t.Fatal("Run() error = nil, want failure after retries are exhausted")
	}
	if provider.attempts != 2 {
		t.Fatalf("provider attempts = %d, want 2 (one try plus one retry)", provider.attempts)
	}
}

func TestRuntimeRetriesCanBeDisabled(t *testing.T) {
	provider := &failingProvider{failures: 99}
	rt := NewRuntime(provider, WithRunRetries(0), WithMaxTurns(5))
	if err := rt.Run(context.Background(), "hi", nil); err == nil {
		t.Fatal("Run() error = nil, want immediate failure")
	}
	if provider.attempts != 1 {
		t.Fatalf("provider attempts = %d, want 1", provider.attempts)
	}
}

func TestSummarizeTodosReportsProgressAndFingerprint(t *testing.T) {
	items := []TodoItem{
		todo("a", TodoCompleted),
		todo("b", TodoInProgress),
		todo("c", TodoPending),
	}
	progress := SummarizeTodos(items)
	if progress.Total != 3 || progress.Completed != 1 {
		t.Fatalf("progress = %+v", progress)
	}
	if progress.InProgress != "b" {
		t.Fatalf("in-progress = %q, want b", progress.InProgress)
	}
	// The next actionable entry is the pending tail, not the in-progress head.
	if progress.Next != "c" {
		t.Fatalf("next = %q, want c", progress.Next)
	}
	if !progress.Outstanding() {
		t.Fatal("Outstanding() = false with items left")
	}

	same := SummarizeTodos(items)
	if same.Fingerprint != progress.Fingerprint {
		t.Fatal("fingerprint is not stable for an unchanged list")
	}
	items[2].Status = TodoCompleted
	if SummarizeTodos(items).Fingerprint == progress.Fingerprint {
		t.Fatal("fingerprint did not change after progress")
	}

	done := SummarizeTodos([]TodoItem{todo("a", TodoCompleted)})
	if done.Outstanding() {
		t.Fatal("Outstanding() = true for a fully completed list")
	}
}

// Plan mode blocks the work itself, so the chain must not push the agent at a
// list it is not allowed to act on.
func TestRuntimeTodoChainHonoursPlanModeGate(t *testing.T) {
	reader := &memTodoReader{}
	reader.set(todo("blocked by plan mode", TodoPending))
	provider := &scriptedProvider{responses: []AssistantResponse{{Content: "here is my plan"}}}

	hook := NewPlanModeHook(true)
	rt := NewRuntime(provider, WithTodoReader(reader), WithHook(hook), WithMaxTurns(10))

	var continuations int
	err := rt.Run(context.Background(), "plan it", func(event Event) {
		if event.Type == EventTodoContinuation {
			continuations++
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if continuations != 0 {
		t.Fatalf("continuations = %d, want 0 while plan mode is on", continuations)
	}

	// Leaving plan mode lets the next run continue the list again.
	hook.SetActive(false)
	if rt.todoChainGated() {
		t.Fatal("todoChainGated() = true after plan mode ended")
	}
	provider2 := &scriptedProvider{responses: []AssistantResponse{
		{Content: "still just talking"},
		{Content: "and again"},
		{Content: "and again"},
		{Content: "and again"},
	}}
	rt2 := NewRuntime(provider2, WithTodoReader(reader), WithHook(hook), WithMaxTurns(10))
	continuations = 0
	if err := rt2.Run(context.Background(), "go", func(event Event) {
		if event.Type == EventTodoContinuation {
			continuations++
		}
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if continuations == 0 {
		t.Fatal("chain did not resume after plan mode was turned off")
	}
}
