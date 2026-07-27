package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// repeatProvider always returns the same single tool call, simulating an
// agent stuck repeating itself.
type repeatProvider struct {
	call ToolCall
}

func (p *repeatProvider) Complete(_ context.Context, _ Request) (AssistantResponse, error) {
	return AssistantResponse{Content: "again", ToolCalls: []ToolCall{p.call}}, nil
}

func (p *repeatProvider) CompleteStream(_ context.Context, _ Request, onDelta func(string)) (AssistantResponse, error) {
	resp := AssistantResponse{Content: "again", ToolCalls: []ToolCall{p.call}}
	if onDelta != nil {
		onDelta(resp.Content)
	}
	return resp, nil
}

func TestRuntimeLoopGuardStopsRepeatedCalls(t *testing.T) {
	provider := &repeatProvider{call: ToolCall{
		ID:        "c1",
		Name:      "uppercase",
		Arguments: json.RawMessage(`{"text":"hi"}`),
	}}
	runtime := NewRuntime(provider, WithTool(uppercaseTool{}), WithHook(NewLoopGuardHook()))

	var stopped bool
	err := runtime.Run(context.Background(), "start", func(event Event) {
		if event.Type == EventHookStopped {
			stopped = true
		}
	})
	if err == nil {
		t.Fatal("expected error from loop guard stop")
	}
	if !stopped {
		t.Fatal("expected EventHookStopped")
	}
	if !strings.Contains(err.Error(), "loop guard") {
		t.Fatalf("err = %v", err)
	}
}

// fakeBashTool is a minimal tool named "bash" so the SafetyHook inspects it.
type fakeBashTool struct{}

func (fakeBashTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "bash",
		Description: "test bash",
		InputSchema: map[string]any{"type": "object"},
	}
}

func (fakeBashTool) Run(_ context.Context, _ ToolInput) (ToolOutput, error) {
	return ToolOutput{Content: "ran"}, nil
}

func TestRuntimeSafetyHookDeniesDestructiveCall(t *testing.T) {
	provider := &scriptedProvider{responses: []AssistantResponse{
		{
			Content: "drop it",
			ToolCalls: []ToolCall{{
				ID:        "c1",
				Name:      "bash",
				Arguments: json.RawMessage(`{"command":"drop database prod"}`),
			}},
		},
		{Content: "ok, I will not drop the database"},
	}}
	runtime := NewRuntime(provider, WithTool(fakeBashTool{}), WithHook(NewSafetyHook("/repo")))

	var denied bool
	err := runtime.Run(context.Background(), "start", func(event Event) {
		if event.Type == EventHookDenied {
			denied = true
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !denied {
		t.Fatal("expected EventHookDenied")
	}
	// The denial should be recorded as a tool result so the agent can recover.
	messages := runtime.Messages()
	found := false
	for _, m := range messages {
		if m.Role == RoleTool && strings.Contains(m.Content, "hook denied") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no denial tool result recorded in messages: %+v", messages)
	}
}

func TestRuntimeWithoutHooksStillWorks(t *testing.T) {
	provider := &scriptedProvider{responses: []AssistantResponse{
		{ToolCalls: []ToolCall{{ID: "c1", Name: "uppercase", Arguments: json.RawMessage(`{"text":"hi"}`)}}},
		{Content: "done"},
	}}
	runtime := NewRuntime(provider, WithTool(uppercaseTool{}))
	err := runtime.Run(context.Background(), "start", func(Event) {})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
