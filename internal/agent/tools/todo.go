package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

const TodoWriteToolName = "todo_write"

// TodoStore persists the planning checklist for the active session.
type TodoStore interface {
	// Load returns the stored todo list as JSON.
	Load() string
	// Save replaces the stored todo list.
	Save(todosJSON string) error
}

// TodoItem and the status values live in the agent package so the run loop can
// read the task list without importing this package. These aliases keep the
// tool-facing API unchanged.
type TodoItem = agent.TodoItem

const (
	TodoPending    = agent.TodoPending
	TodoInProgress = agent.TodoInProgress
	TodoCompleted  = agent.TodoCompleted
)

// TodoWriteTool lets the agent keep an explicit task list for multi-step work.
// The list is persisted on the session and rendered by the UI.
type TodoWriteTool struct {
	store        TodoStore
	instructions string
}

type todoWriteArgs struct {
	Todos []TodoItem `json:"todos"`
}

func NewTodoWriteTool(options TodoWriteOptions) *TodoWriteTool {
	return &TodoWriteTool{store: options.Store}
}

// TodoWriteOptions configures the todo tool.
type TodoWriteOptions struct {
	Store TodoStore
}

func (t *TodoWriteTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        TodoWriteToolName,
		Description: "Record or update the task list for the current work. Pass the complete list every time; it replaces the previous one.",
		Prompt: "Use this tool to plan and track multi-step work. Send the full list on every call: " +
			"entries marked completed stay visible so progress is explicit. Exactly one entry should be " +
			"in_progress while work is ongoing. Statuses: pending, in_progress, completed.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type":        "array",
					"description": "The complete task list, in order.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content": map[string]any{"type": "string", "description": "Short imperative task description."},
							"status": map[string]any{
								"type": "string",
								"enum": []string{TodoPending, TodoInProgress, TodoCompleted},
							},
						},
						"required": []string{"content", "status"},
					},
				},
			},
			"required": []string{"todos"},
		},
		// Writing a task list is not dangerous and needs no approval.
		Behavior: agent.ToolBehavior{ReadOnly: false},
	}
}

func (t *TodoWriteTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args todoWriteArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("todo_write: invalid args: %w", err)
	}
	for i := range args.Todos {
		args.Todos[i].Content = strings.TrimSpace(args.Todos[i].Content)
		args.Todos[i].Status = normalizeTodoStatus(args.Todos[i].Status)
		if args.Todos[i].Content == "" {
			return agent.ToolOutput{}, fmt.Errorf("todo_write: todos[%d].content is required", i)
		}
	}
	if len(args.Todos) > 1 {
		inProgress := 0
		for _, item := range args.Todos {
			if item.Status == TodoInProgress {
				inProgress++
			}
		}
		if inProgress > 1 {
			return agent.ToolOutput{}, fmt.Errorf("todo_write: at most one task may be in_progress, got %d", inProgress)
		}
	}

	data, err := json.Marshal(args.Todos)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("todo_write: %w", err)
	}
	if t.store != nil {
		if err := t.store.Save(string(data)); err != nil {
			return agent.ToolOutput{}, fmt.Errorf("todo_write: %w", err)
		}
	}
	rendered := RenderTodos(args.Todos)
	return agent.ToolOutput{
		Content: rendered,
		Metadata: map[string]any{
			"todos":    string(data),
			"rendered": rendered,
			"count":    len(args.Todos),
		},
	}, nil
}

func normalizeTodoStatus(status string) string {
	return agent.NormalizeTodoStatus(status)
}

// RenderTodos renders a todo list as a compact checklist. Done items are kept
// so the user can see what has already happened.
func RenderTodos(items []TodoItem) string {
	return agent.RenderTodos(items)
}

// ParseTodos decodes a stored todo list, returning nil for empty or invalid
// payloads.
func ParseTodos(raw string) []TodoItem {
	return agent.ParseTodos(raw)
}
