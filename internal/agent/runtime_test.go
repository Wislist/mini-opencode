package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
