package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistryToolServiceListsTools(t *testing.T) {
	registry := NewToolRegistry()
	if err := registry.Register(serviceTestTool{}); err != nil {
		t.Fatal(err)
	}

	service := NewRegistryToolService(registry)
	tools := service.ListTools()
	if len(tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(tools))
	}
	if tools[0].Name != "service_test" {
		t.Fatalf("tool name = %s", tools[0].Name)
	}
}

func TestRegistryToolServiceEmitsStartedAndFinished(t *testing.T) {
	registry := NewToolRegistry()
	if err := registry.Register(serviceTestTool{}); err != nil {
		t.Fatal(err)
	}

	service := NewRegistryToolService(registry)
	events, err := service.RunTool(context.Background(), ToolRunRequest{Call: ToolCall{
		ID:        "call-1",
		Name:      "service_test",
		Arguments: json.RawMessage(`{}`),
	}})
	if err != nil {
		t.Fatalf("RunTool() error = %v", err)
	}

	got := collectToolEvents(events)
	if len(got) != 2 {
		t.Fatalf("event count = %d, want 2", len(got))
	}
	if got[0].Type != ToolEventStarted || got[1].Type != ToolEventFinished {
		t.Fatalf("events = %#v", got)
	}
	if got[1].Result.Content != "ok" {
		t.Fatalf("result = %#v", got[1].Result)
	}
}

func TestRegistryToolServiceEmitsPermissionRequired(t *testing.T) {
	registry := NewToolRegistry()
	registry.SetPermissionPolicy(NewDefaultPermissionPolicy(t.TempDir()))
	if err := registry.Register(permissionTestTool{}); err != nil {
		t.Fatal(err)
	}

	service := NewRegistryToolService(registry)
	events, err := service.RunTool(context.Background(), ToolRunRequest{Call: ToolCall{
		ID:        "call-1",
		Name:      "write_like",
		Arguments: json.RawMessage(`{"path":"file.txt"}`),
	}})
	if err != nil {
		t.Fatalf("RunTool() error = %v", err)
	}

	got := collectToolEvents(events)
	if len(got) != 2 {
		t.Fatalf("event count = %d, want 2", len(got))
	}
	if got[1].Type != ToolEventPermissionRequired {
		t.Fatalf("event = %#v", got[1])
	}
	if got[1].Error == nil {
		t.Fatal("expected permission error")
	}
}

func TestRegistryToolServiceRunsApprovedTool(t *testing.T) {
	registry := NewToolRegistry()
	registry.SetPermissionPolicy(NewDefaultPermissionPolicy(t.TempDir()))
	if err := registry.Register(permissionTestTool{}); err != nil {
		t.Fatal(err)
	}

	service := NewRegistryToolService(registry)
	events, err := service.RunTool(context.Background(), ToolRunRequest{
		Approved: true,
		Call: ToolCall{
			ID:        "call-1",
			Name:      "write_like",
			Arguments: json.RawMessage(`{"path":"file.txt"}`),
		},
	})
	if err != nil {
		t.Fatalf("RunTool() error = %v", err)
	}

	got := collectToolEvents(events)
	if len(got) != 2 {
		t.Fatalf("event count = %d, want 2", len(got))
	}
	if got[1].Type != ToolEventFinished || got[1].Result.Content != "ran" {
		t.Fatalf("event = %#v", got[1])
	}
}

type serviceTestTool struct{}

func (serviceTestTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "service_test",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
		Behavior:    ToolBehavior{ReadOnly: true},
	}
}

func (serviceTestTool) Run(ctx context.Context, input ToolInput) (ToolOutput, error) {
	return ToolOutput{Content: "ok"}, nil
}

func collectToolEvents(events <-chan ToolEvent) []ToolEvent {
	var out []ToolEvent
	for event := range events {
		out = append(out, event)
	}
	return out
}
