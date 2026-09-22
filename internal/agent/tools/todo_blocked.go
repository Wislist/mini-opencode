package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

const TodoBlockedToolName = "todo_blocked"

// TodoBlockedTool lets the agent stop the todo chain deliberately: instead of
// silently ending a turn with work left, it reports what blocks it and what it
// needs. The runtime treats this as the agent's judgement that the remaining
// items cannot proceed and ends the chain cleanly.
type TodoBlockedTool struct{}

func NewTodoBlockedTool() *TodoBlockedTool { return &TodoBlockedTool{} }

func (t *TodoBlockedTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        TodoBlockedToolName,
		Description: "Report that the remaining task list cannot proceed and stop the automatic continuation. Use it only for a real blocker.",
		Prompt: "Call this when you genuinely cannot continue: missing information only the user has, a " +
			"decision that is the user's to make, missing credentials or access, or a failing dependency " +
			"outside your control. State the blocker and what you need to resume. Do not use it to ask for " +
			"permission to do work you can already do, and do not use it while you can still make progress.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{
					"type":        "string",
					"description": "What blocks the remaining work.",
				},
				"needs": map[string]any{
					"type":        "string",
					"description": "What you need from the user to resume.",
				},
				"remaining": map[string]any{
					"type":        "array",
					"description": "Task list entries you could not complete.",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"reason"},
		},
		// Reporting a blocker changes no files and must stay available even in
		// plan mode.
		Behavior: agent.ToolBehavior{ReadOnly: true},
	}
}

type todoBlockedArgs struct {
	Reason    string   `json:"reason"`
	Needs     string   `json:"needs"`
	Remaining []string `json:"remaining"`
}

func (t *TodoBlockedTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args todoBlockedArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("todo_blocked: invalid args: %w", err)
	}
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return agent.ToolOutput{}, fmt.Errorf("todo_blocked: reason is required")
	}
	needs := strings.TrimSpace(args.Needs)

	content := "blocker reported: " + reason
	if needs != "" {
		content += "\nneeds: " + needs
	}
	content += "\n\nThe task list is paused; the user can resume by sending a message."

	metadata := map[string]any{
		"blocked": true,
		"reason":  reason,
	}
	if needs != "" {
		metadata["needs"] = needs
	}
	if len(args.Remaining) > 0 {
		metadata["remaining"] = args.Remaining
	}
	return agent.ToolOutput{Content: content, Metadata: metadata}, nil
}
