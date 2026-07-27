package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

type fakeClient struct {
	result CallToolResult
}

func (c fakeClient) Initialize(ctx context.Context) error { return nil }
func (c fakeClient) ListTools(ctx context.Context) ([]ToolDef, error) {
	return nil, nil
}
func (c fakeClient) CallTool(ctx context.Context, name string, args json.RawMessage) (CallToolResult, error) {
	return c.result, nil
}
func (c fakeClient) Close() error { return nil }

func TestToolAdapterReturnsTextContent(t *testing.T) {
	adapter := NewToolAdapter(fakeClient{
		result: CallToolResult{
			Content: []ToolContent{
				{Type: "text", Text: "hello"},
				{Type: "text", Text: "world"},
			},
		},
	}, ToolDef{Name: "go_doc", Description: "Go docs"})

	got, err := adapter.Run(context.Background(), agent.ToolInput{
		CallID:    "call-1",
		Name:      "go_doc",
		Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.Content != "hello\nworld" {
		t.Fatalf("Run() = %q", got)
	}
}
