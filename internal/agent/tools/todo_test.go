package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

type fakeTodoStore struct {
	saved string
}

func (s *fakeTodoStore) Load() string            { return s.saved }
func (s *fakeTodoStore) Save(todos string) error { s.saved = todos; return nil }

func TestTodoWritePersistsAndRenders(t *testing.T) {
	store := &fakeTodoStore{}
	tool := NewTodoWriteTool(TodoWriteOptions{Store: store})

	args, _ := json.Marshal(map[string]any{
		"todos": []map[string]any{
			{"content": "wire mcp", "status": "completed"},
			{"content": "add todo tool", "status": "in_progress"},
			{"content": "docs", "status": "pending"},
		},
	})
	out, err := tool.Run(context.Background(), agent.ToolInput{Arguments: args})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "[x] wire mcp") {
		t.Fatalf("rendered content = %q", out.Content)
	}
	if !strings.Contains(out.Content, "[>] add todo tool") {
		t.Fatalf("in-progress marker missing: %q", out.Content)
	}
	if !strings.Contains(out.Content, "(1/3 done)") {
		t.Fatalf("progress summary missing: %q", out.Content)
	}
	if _, ok := out.Metadata["todos"]; !ok {
		t.Fatalf("metadata = %#v, want todos key for the runtime event", out.Metadata)
	}

	items := ParseTodos(store.saved)
	if len(items) != 3 || items[1].Status != TodoInProgress {
		t.Fatalf("stored todos = %#v", items)
	}
}

func TestTodoWriteRejectsMultipleInProgress(t *testing.T) {
	tool := NewTodoWriteTool(TodoWriteOptions{Store: &fakeTodoStore{}})
	args, _ := json.Marshal(map[string]any{
		"todos": []map[string]any{
			{"content": "a", "status": "in_progress"},
			{"content": "b", "status": "in_progress"},
		},
	})
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: args}); err == nil {
		t.Fatal("Run() error = nil, want rejection of two in-progress tasks")
	}
}

func TestTodoWriteNormalizesStatusAndRejectsEmptyContent(t *testing.T) {
	tool := NewTodoWriteTool(TodoWriteOptions{Store: &fakeTodoStore{}})
	args, _ := json.Marshal(map[string]any{
		"todos": []map[string]any{{"content": "ship it", "status": "DONE"}},
	})
	out, err := tool.Run(context.Background(), agent.ToolInput{Arguments: args})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "[x] ship it") {
		t.Fatalf("status was not normalized: %q", out.Content)
	}

	empty, _ := json.Marshal(map[string]any{"todos": []map[string]any{{"content": "  ", "status": "pending"}}})
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: empty}); err == nil {
		t.Fatal("Run() error = nil, want rejection of empty content")
	}
}
