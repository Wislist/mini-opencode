package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedProvider struct {
	responses []AssistantResponse
	calls     int
}

func (p *scriptedProvider) Complete(ctx context.Context, req Request) (AssistantResponse, error) {
	if p.calls >= len(p.responses) {
		return AssistantResponse{Content: "done"}, nil
	}
	resp := p.responses[p.calls]
	p.calls++
	return resp, nil
}

// CompleteStream delegates to Complete, emitting the full content as a single
// delta so the scripted provider satisfies the streaming interface in tests.
func (p *scriptedProvider) CompleteStream(ctx context.Context, req Request, onDelta func(string)) (AssistantResponse, error) {
	resp, err := p.Complete(ctx, req)
	if err == nil && onDelta != nil && resp.Content != "" {
		onDelta(resp.Content)
	}
	return resp, err
}

type uppercaseTool struct{}

func (t uppercaseTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "uppercase",
		Description: "Uppercase input text.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []string{"text"},
		},
		Behavior: ToolBehavior{ReadOnly: true},
	}
}

func (t uppercaseTool) Run(ctx context.Context, toolInput ToolInput) (ToolOutput, error) {
	var args struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(toolInput.Arguments, &args); err != nil {
		return ToolOutput{}, err
	}
	return ToolOutput{Content: strings.ToUpper(args.Text)}, nil
}

func TestRuntimeExecutesToolAndContinues(t *testing.T) {
	provider := &scriptedProvider{
		responses: []AssistantResponse{
			{
				Content: "using tool",
				ToolCalls: []ToolCall{
					{
						ID:        "call-1",
						Name:      "uppercase",
						Arguments: json.RawMessage(`{"text":"hello"}`),
					},
				},
			},
			{Content: "finished"},
		},
	}
	runtime := NewRuntime(provider, WithTool(uppercaseTool{}))

	var events []EventType
	err := runtime.Run(context.Background(), "start", func(event Event) {
		events = append(events, event.Type)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	messages := runtime.Messages()
	if len(messages) != 4 {
		t.Fatalf("message count = %d, want 4", len(messages))
	}
	if messages[2].Role != RoleTool || messages[2].Content != "HELLO" {
		t.Fatalf("tool message = %#v", messages[2])
	}
	if events[len(events)-1] != EventRunFinished {
		t.Fatalf("last event = %s, want %s", events[len(events)-1], EventRunFinished)
	}
}

func TestRuntimeEmitsToolPermissionRequired(t *testing.T) {
	provider := &scriptedProvider{
		responses: []AssistantResponse{
			{
				Content: "using tool",
				ToolCalls: []ToolCall{
					{
						ID:        "call-1",
						Name:      "write_like",
						Arguments: json.RawMessage(`{"path":"file.txt"}`),
					},
				},
			},
			{Content: "finished"},
		},
	}
	runtime := NewRuntime(
		provider,
		WithTool(permissionTestTool{}),
		WithPermissionPolicy(NewDefaultPermissionPolicy(t.TempDir())),
	)

	var events []EventType
	err := runtime.Run(context.Background(), "start", func(event Event) {
		events = append(events, event.Type)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if !hasEvent(events, EventToolPermissionRequired) {
		t.Fatalf("events = %#v", events)
	}
}

func TestRuntimeExecutesToolAfterPermissionConfirmation(t *testing.T) {
	provider := &scriptedProvider{
		responses: []AssistantResponse{
			{
				Content: "using tool",
				ToolCalls: []ToolCall{
					{
						ID:        "call-1",
						Name:      "write_like",
						Arguments: json.RawMessage(`{"path":"file.txt"}`),
					},
				},
			},
			{Content: "finished"},
		},
	}
	runtime := NewRuntime(
		provider,
		WithTool(permissionTestTool{}),
		WithPermissionPolicy(NewDefaultPermissionPolicy(t.TempDir())),
		WithPermissionConfirmer(func(ctx context.Context, call ToolCall, result ToolResult) bool { return true }),
	)

	err := runtime.Run(context.Background(), "start", func(event Event) {})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	messages := runtime.Messages()
	if len(messages) < 3 || messages[2].Content != "ran" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestRuntimeEmitsSingleToolStartAfterPermissionConfirmation(t *testing.T) {
	provider := &scriptedProvider{
		responses: []AssistantResponse{
			{
				Content: "using tool",
				ToolCalls: []ToolCall{{
					ID:        "call-1",
					Name:      "write_like",
					Arguments: json.RawMessage(`{"path":"file.txt"}`),
				}},
			},
			{Content: "finished"},
		},
	}
	runtime := NewRuntime(
		provider,
		WithTool(permissionTestTool{}),
		WithPermissionPolicy(NewDefaultPermissionPolicy(t.TempDir())),
		WithPermissionConfirmer(func(ctx context.Context, call ToolCall, result ToolResult) bool { return true }),
	)

	started := 0
	err := runtime.Run(context.Background(), "start", func(event Event) {
		if event.Type == EventToolCallStarted {
			started++
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if started != 1 {
		t.Fatalf("tool started events = %d, want 1", started)
	}
}

func hasEvent(events []EventType, want EventType) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func TestRuntimeCompactSummarizesAndReplacesMessages(t *testing.T) {
	provider := &scriptedProvider{
		responses: []AssistantResponse{
			{Content: "using tool", ToolCalls: []ToolCall{{
				ID: "call-1", Name: "uppercase",
				Arguments: json.RawMessage(`{"text":"hello"}`),
			}}},
			{Content: "finished"},
			{Content: "SUMMARY: user uppercased hello"},
		},
	}
	runtime := NewRuntime(provider, WithTool(uppercaseTool{}))

	if err := runtime.Run(context.Background(), "start", func(event Event) {}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	before := len(runtime.Messages())
	if before < 4 {
		t.Fatalf("before compact message count = %d, want >= 4", before)
	}

	summary, err := runtime.Compact(context.Background(), "summarize the conversation")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if summary != "SUMMARY: user uppercased hello" {
		t.Fatalf("summary = %q", summary)
	}

	after := runtime.Messages()
	if len(after) != 1 {
		t.Fatalf("after compact message count = %d, want 1", len(after))
	}
	if after[0].Role != RoleUser {
		t.Fatalf("after compact role = %s, want user", after[0].Role)
	}
	if !strings.Contains(after[0].Content, "SUMMARY: user uppercased hello") {
		t.Fatalf("after compact content = %q", after[0].Content)
	}
	if !strings.Contains(after[0].Content, "<conversation_summary>") {
		t.Fatalf("after compact content missing summary tags: %q", after[0].Content)
	}
}

func TestRuntimeCompactDetailedReturnsUserSummarySection(t *testing.T) {
	provider := &scriptedProvider{
		responses: []AssistantResponse{{Content: strings.Join([]string{
			"## Current State",
			"Internal handoff details are preserved here.",
			"## User Summary",
			"- 已保留当前 /compact 修改进度。",
			"- 接下来会继续验证 compact 后的显示行为。",
			"```go",
			"fmt.Println(\"do not show\")",
			"```",
			"## Exact Next Steps",
			"1. Run tests.",
		}, "\n")}},
	}
	runtime := NewRuntime(provider)
	runtime.SetMessages([]Message{{Role: RoleUser, Content: "请修改 /compact"}})

	result, err := runtime.CompactDetailed(context.Background(), "summarize")
	if err != nil {
		t.Fatalf("CompactDetailed() error = %v", err)
	}
	if !strings.Contains(result.Summary, "Internal handoff details") {
		t.Fatalf("full summary not preserved: %q", result.Summary)
	}
	if !strings.Contains(result.UserSummary, "已保留当前 /compact 修改进度") {
		t.Fatalf("user summary missing expected bullet: %q", result.UserSummary)
	}
	for _, unwanted := range []string{"do not show", "Exact Next Steps"} {
		if strings.Contains(result.UserSummary, unwanted) {
			t.Fatalf("user summary leaked %q: %q", unwanted, result.UserSummary)
		}
	}
}

func TestRuntimeCompactNoMessagesIsNoop(t *testing.T) {
	provider := &scriptedProvider{}
	runtime := NewRuntime(provider)

	summary, err := runtime.Compact(context.Background(), "summarize")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty", summary)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestRuntimeContextEstimateGrowsWithMessages(t *testing.T) {
	runtime := NewRuntime(&scriptedProvider{}, WithSystemPrompt("1234"))

	before := runtime.ContextEstimate()
	if before < 1 {
		t.Fatalf("before = %d, want >= 1", before)
	}

	if err := runtime.Run(context.Background(), "a sufficiently long user message", func(Event) {}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	after := runtime.ContextEstimate()
	if after <= before {
		t.Fatalf("after = %d, before = %d, want after > before", after, before)
	}
}

func TestRuntimeSetMessagesReplacesHistory(t *testing.T) {
	runtime := NewRuntime(&scriptedProvider{})

	if err := runtime.Run(context.Background(), "first message", func(Event) {}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	original := runtime.Messages()
	if len(original) < 2 {
		t.Fatalf("original message count = %d, want >= 2", len(original))
	}

	replacement := []Message{
		{Role: RoleUser, Content: "restored message"},
		{Role: RoleAssistant, Content: "restored reply"},
	}
	runtime.SetMessages(replacement)

	got := runtime.Messages()
	if len(got) != 2 {
		t.Fatalf("after SetMessages count = %d, want 2", len(got))
	}
	if got[0].Content != "restored message" {
		t.Errorf("first message = %q", got[0].Content)
	}
	if got[1].Content != "restored reply" {
		t.Errorf("second message = %q", got[1].Content)
	}
}

func TestRuntimeSetMessagesNilClearsHistory(t *testing.T) {
	runtime := NewRuntime(&scriptedProvider{})

	if err := runtime.Run(context.Background(), "temp message", func(Event) {}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(runtime.Messages()) == 0 {
		t.Fatal("expected messages before clear")
	}

	runtime.SetMessages(nil)

	if len(runtime.Messages()) != 0 {
		t.Fatalf("after SetMessages(nil) count = %d, want 0", len(runtime.Messages()))
	}
}

// metadataTool emits a tool result carrying caller-supplied metadata, which is
// how the todo and plan tools signal the runtime.
type metadataTool struct {
	name     string
	metadata map[string]any
	readOnly bool
}

func (t metadataTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
		Behavior:    ToolBehavior{ReadOnly: t.readOnly},
	}
}

func (t metadataTool) Run(_ context.Context, _ ToolInput) (ToolOutput, error) {
	return ToolOutput{Content: "ok", Metadata: t.metadata}, nil
}

func toolCallResponse(name string) AssistantResponse {
	args, _ := json.Marshal(map[string]any{"text": "hi"})
	return AssistantResponse{
		ToolCalls: []ToolCall{{ID: "call-1", Name: name, Arguments: args}},
	}
}

func TestRuntimeEmitsTodosChangedForTodoMetadata(t *testing.T) {
	provider := &scriptedProvider{responses: []AssistantResponse{
		toolCallResponse("todo_write"),
		{Content: "all done"},
	}}
	rt := NewRuntime(provider, WithTool(metadataTool{
		name: "todo_write",
		metadata: map[string]any{
			"todos":    `[{"content":"ship","status":"pending"}]`,
			"rendered": "[ ] ship\n(0/1 done)",
		},
	}))

	var todos []string
	err := rt.Run(context.Background(), "plan it", func(event Event) {
		if event.Type == EventTodosChanged {
			todos = append(todos, event.Todos)
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(todos) != 1 || !strings.Contains(todos[0], "ship") {
		t.Fatalf("todos events = %#v", todos)
	}
}

func TestRuntimeEmitsPlanSubmittedForPlanMetadata(t *testing.T) {
	provider := &scriptedProvider{responses: []AssistantResponse{
		toolCallResponse("exit_plan_mode"),
		{Content: "implementing"},
	}}
	rt := NewRuntime(provider, WithTool(metadataTool{
		name:     "exit_plan_mode",
		readOnly: true,
		metadata: map[string]any{"plan": "1. do it", "approved": true},
	}))

	var plans []string
	err := rt.Run(context.Background(), "plan it", func(event Event) {
		if event.Type == EventPlanSubmitted {
			plans = append(plans, event.Plan)
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(plans) != 1 || plans[0] != "1. do it" {
		t.Fatalf("plan events = %#v", plans)
	}
}

func TestRuntimeAccumulatesUsageAcrossTurns(t *testing.T) {
	// The first turn must call a tool, otherwise a text-only response ends the
	// run and there is no second completion to accumulate.
	first := toolCallResponse("uppercase")
	first.Usage = Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14}
	provider := &scriptedProvider{responses: []AssistantResponse{
		first,
		{Content: "second", Usage: Usage{PromptTokens: 6, CompletionTokens: 2, TotalTokens: 8}},
	}}
	rt := NewRuntime(provider, WithTool(uppercaseTool{}))

	var usages []Usage
	if err := rt.Run(context.Background(), "hi", func(event Event) {
		if event.Type == EventUsage && event.Usage != nil {
			usages = append(usages, *event.Usage)
		}
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(usages) != 2 {
		t.Fatalf("usage events = %#v", usages)
	}
	if got := rt.Usage(); got.PromptTokens != 16 || got.CompletionTokens != 6 || got.TotalTokens != 22 {
		t.Fatalf("accumulated usage = %+v", got)
	}

	// A restore replaces the totals, which is what session switching relies on.
	rt.SetUsage(Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4})
	if got := rt.Usage(); got.TotalTokens != 4 {
		t.Fatalf("usage after restore = %+v", got)
	}
}

// concurrencyProbeTool is a read-only tool that records how many instances run
// at the same time, so the test can prove the runtime batches them.
type concurrencyProbeTool struct {
	name string

	mu      sync.Mutex
	active  int
	maxSeen int
	order   *[]string
}

func (t *concurrencyProbeTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        t.name,
		Description: "concurrency probe",
		InputSchema: map[string]any{"type": "object"},
		Behavior:    ToolBehavior{ReadOnly: true},
	}
}

func (t *concurrencyProbeTool) Run(_ context.Context, input ToolInput) (ToolOutput, error) {
	t.mu.Lock()
	t.active++
	if t.active > t.maxSeen {
		t.maxSeen = t.active
	}
	t.mu.Unlock()

	time.Sleep(20 * time.Millisecond)

	var args struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(input.Arguments, &args)

	t.mu.Lock()
	if t.order != nil {
		*t.order = append(*t.order, args.Text)
	}
	t.active--
	t.mu.Unlock()
	return ToolOutput{Content: strings.ToUpper(args.Text)}, nil
}

func (t *concurrencyProbeTool) maxConcurrent() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maxSeen
}

func TestRuntimeRunsReadOnlyToolsConcurrently(t *testing.T) {
	tool := &concurrencyProbeTool{name: "probe"}
	provider := &scriptedProvider{responses: []AssistantResponse{{
		ToolCalls: []ToolCall{
			{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{"text":"a"}`)},
			{ID: "c2", Name: "probe", Arguments: json.RawMessage(`{"text":"b"}`)},
			{ID: "c3", Name: "probe", Arguments: json.RawMessage(`{"text":"c"}`)},
		},
	}, {Content: "done"}}}

	rt := NewRuntime(provider, WithTool(tool))

	start := time.Now()
	if err := rt.Run(context.Background(), "go", func(Event) {}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	elapsed := time.Since(start)

	if tool.maxConcurrent() < 2 {
		t.Fatalf("max concurrent read-only tools = %d, want >= 2", tool.maxConcurrent())
	}
	// Three 20ms probes in parallel must finish well under the serial 60ms.
	if elapsed >= 55*time.Millisecond {
		t.Fatalf("elapsed = %v, want parallel execution under 55ms", elapsed)
	}

	// Results are recorded in call order regardless of completion order.
	messages := rt.Messages()
	var contents []string
	for _, msg := range messages {
		if msg.Role == RoleTool {
			contents = append(contents, msg.Content)
		}
	}
	if len(contents) != 3 || contents[0] != "A" || contents[1] != "B" || contents[2] != "C" {
		t.Fatalf("tool results = %#v, want ordered A, B, C", contents)
	}
}

func TestRuntimeKeepsWriteToolsSerialAfterReadOnlyGroup(t *testing.T) {
	probe := &concurrencyProbeTool{name: "probe"}
	provider := &scriptedProvider{responses: []AssistantResponse{{
		ToolCalls: []ToolCall{
			{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{"text":"a"}`)},
			{ID: "c2", Name: "probe", Arguments: json.RawMessage(`{"text":"b"}`)},
			{ID: "c3", Name: "write_like", Arguments: json.RawMessage(`{"path":"file.txt"}`)},
		},
	}, {Content: "done"}}}

	rt := NewRuntime(
		provider,
		WithTool(probe),
		WithTool(permissionTestTool{}),
		WithPermissionPolicy(NewDefaultPermissionPolicy(t.TempDir())),
		WithPermissionConfirmer(func(context.Context, ToolCall, ToolResult) bool { return true }),
	)

	if err := rt.Run(context.Background(), "go", func(Event) {}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var contents []string
	for _, msg := range rt.Messages() {
		if msg.Role == RoleTool {
			contents = append(contents, msg.Content)
		}
	}
	if len(contents) != 3 || contents[0] != "A" || contents[1] != "B" || contents[2] != "ran" {
		t.Fatalf("tool results = %#v, want A, B, ran", contents)
	}
}

func TestContextTokensPrefersProviderReportedPromptSize(t *testing.T) {
	rt := NewRuntime(&scriptedProvider{})
	rt.SetMessages([]Message{{Role: RoleUser, Content: "short"}})

	estimate := rt.ContextEstimate()
	if got := rt.ContextTokens(); got != estimate {
		t.Fatalf("ContextTokens() = %d, want the estimate %d", got, estimate)
	}
	// A provider-reported prompt size larger than the estimate wins, because it
	// reflects the real tokenization.
	rt.SetContextTokens(estimate + 5000)
	if got := rt.ContextTokens(); got != estimate+5000 {
		t.Fatalf("ContextTokens() = %d, want the provider number", got)
	}
	// A stale, smaller provider number must not shrink the reported context
	// below the local estimate.
	rt.SetContextTokens(1)
	if got := rt.ContextTokens(); got != estimate {
		t.Fatalf("ContextTokens() = %d, want the larger of the two (%d)", got, estimate)
	}
}

func TestContextEstimateCountsCJKRunes(t *testing.T) {
	rt := NewRuntime(&scriptedProvider{})
	// 40 CJK characters are ~40 tokens; the old byte/4 heuristic would have
	// reported ~30 for the same string (3 bytes per rune / 4).
	rt.SetMessages([]Message{{Role: RoleUser, Content: strings.Repeat("中", 40)}})
	if got := rt.ContextEstimate(); got < 40 {
		t.Fatalf("ContextEstimate() = %d, want at least one token per CJK rune", got)
	}
}

func TestRuntimeAutoCompactsWhenContextNearsWindow(t *testing.T) {
	first := toolCallResponse("uppercase")
	first.Usage = Usage{PromptTokens: 90, CompletionTokens: 5, TotalTokens: 95}
	provider := &scriptedProvider{responses: []AssistantResponse{
		first,
		{Content: "## User summary\nWork continues from the summary."},
		{Content: "finished after compaction"},
	}}
	rt := NewRuntime(provider,
		WithTool(uppercaseTool{}),
		WithContextWindow(100),
		WithCompactionPrompt("summarize the conversation"),
	)

	var compacted []Event
	err := rt.Run(context.Background(), "long task", func(event Event) {
		if event.Type == EventContextCompacted {
			compacted = append(compacted, event)
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(compacted) != 1 {
		t.Fatalf("compaction events = %#v", compacted)
	}
	if compacted[0].Error != nil {
		t.Fatalf("compaction failed: %v", compacted[0].Error)
	}
	if compacted[0].ContextTokens < 90 {
		t.Fatalf("event context size = %d, want the provider-reported size", compacted[0].ContextTokens)
	}
	if rt.AutoCompactions() != 1 {
		t.Fatalf("AutoCompactions() = %d, want 1", rt.AutoCompactions())
	}
	// The transcript was replaced by the single summary message.
	messages := rt.Messages()
	if len(messages) != 2 {
		t.Fatalf("messages after compaction = %d, want the summary plus the final turn", len(messages))
	}
	if !strings.Contains(messages[0].Content, "<conversation_summary>") {
		t.Fatalf("first message is not the summary: %q", messages[0].Content)
	}
}

func TestRuntimeDoesNotCompactBelowThreshold(t *testing.T) {
	first := toolCallResponse("uppercase")
	first.Usage = Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	provider := &scriptedProvider{responses: []AssistantResponse{
		first,
		{Content: "done"},
	}}
	rt := NewRuntime(provider,
		WithTool(uppercaseTool{}),
		WithContextWindow(1000),
		WithCompactionPrompt("summarize the conversation"),
	)

	if err := rt.Run(context.Background(), "small task", nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if rt.AutoCompactions() != 0 {
		t.Fatalf("AutoCompactions() = %d, want 0", rt.AutoCompactions())
	}
}

// A compaction failure must not abort the run: the request is still worth
// attempting and the user can compact by hand.
func TestRuntimeSurvivesCompactionFailure(t *testing.T) {
	first := toolCallResponse("uppercase")
	first.Usage = Usage{PromptTokens: 900, CompletionTokens: 5, TotalTokens: 905}
	provider := &scriptedProvider{responses: []AssistantResponse{
		first,
		{Content: ""}, // empty summary -> CompactDetailed fails
		{Content: "continued anyway"},
	}}
	rt := NewRuntime(provider,
		WithTool(uppercaseTool{}),
		WithContextWindow(1000),
		WithCompactionPrompt("summarize"),
	)

	var failed bool
	err := rt.Run(context.Background(), "task", func(event Event) {
		if event.Type == EventContextCompacted && event.Error != nil {
			failed = true
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want the run to survive a compaction failure", err)
	}
	if !failed {
		t.Fatal("compaction failure was not reported")
	}
}

func TestRuntimeReportsTurnBudgetExhaustion(t *testing.T) {
	provider := &scriptedProvider{responses: []AssistantResponse{
		toolCallResponse("uppercase"),
		toolCallResponse("uppercase"),
		toolCallResponse("uppercase"),
	}}
	rt := NewRuntime(provider, WithTool(uppercaseTool{}), WithMaxTurns(2))

	var budget bool
	err := rt.Run(context.Background(), "loop", func(event Event) {
		if event.Type == EventBudgetExhausted {
			budget = true
		}
	})
	if err == nil {
		t.Fatal("Run() error = nil, want budget exhaustion")
	}
	if !strings.Contains(err.Error(), "turn budget exhausted") {
		t.Fatalf("error = %v, want an actionable message", err)
	}
	if !budget {
		t.Fatal("EventBudgetExhausted was not emitted")
	}
}

// A summary that is still larger than the threshold must not make the runtime
// compact on every turn: growth beyond the post-compaction size is required.
func TestRuntimeDoesNotCompactRepeatedlyWithoutGrowth(t *testing.T) {
	compact := AssistantResponse{Content: "## User summary\n" + strings.Repeat("detail ", 30)}
	provider := &scriptedProvider{responses: []AssistantResponse{
		func() AssistantResponse {
			first := toolCallResponse("uppercase")
			first.Usage = Usage{PromptTokens: 90, CompletionTokens: 5, TotalTokens: 95}
			return first
		}(),
		compact,                       // first automatic compaction
		toolCallResponse("uppercase"), // turn 2 still calls a tool
		compact,                       // would be the second compaction
		{Content: "finished"},
	}}
	rt := NewRuntime(provider,
		WithTool(uppercaseTool{}),
		WithContextWindow(100),
		WithCompactionPrompt("summarize"),
	)

	if err := rt.Run(context.Background(), "long task", nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := rt.AutoCompactions(); got != 1 {
		t.Fatalf("AutoCompactions() = %d, want 1: repeated compaction must require growth", got)
	}
}

// The UI reads runtime state from another goroutine while a run is in flight.
// Run this package with -race to verify the locking actually holds.
func TestRuntimeConcurrentObservationIsRaceFree(t *testing.T) {
	provider := &scriptedProvider{responses: []AssistantResponse{
		toolCallResponse("uppercase"),
		toolCallResponse("uppercase"),
		{Content: "done", Usage: Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15}},
	}}
	rt := NewRuntime(provider,
		WithTool(uppercaseTool{}),
		WithContextWindow(100000),
		WithCompactionPrompt("summarize"),
	)

	done := make(chan error, 1)
	go func() {
		done <- rt.Run(context.Background(), "work", func(Event) {})
	}()

	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			return
		default:
		}
		// Every accessor below is called from this goroutine while Run mutates
		// the same state in the other one.
		_ = rt.Messages()
		_ = rt.Usage()
		_ = rt.ContextEstimate()
		_ = rt.ContextTokens()
		_ = rt.AutoCompactions()
		_ = rt.SystemPrompt()
		_ = rt.Tools()
		rt.SetContextTokens(0)
		rt.SetUsage(rt.Usage())
	}
}

func TestToolRegistrySortsNamesAndStaysRaceFree(t *testing.T) {
	registry := NewToolRegistry()
	register := func(name string) {
		if err := registry.Register(metadataTool{name: name}); err != nil {
			t.Fatalf("Register(%s) error = %v", name, err)
		}
	}
	register("zulu")
	register("alpha")
	register("mike")

	specs := registry.Specs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	if strings.Join(names, ",") != "alpha,mike,zulu" {
		t.Fatalf("names = %v, want sorted order", names)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = registry.Specs()
				_, _ = registry.Definition("alpha")
			}
		}()
	}
	wg.Wait()
}
