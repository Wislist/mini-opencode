package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

const TaskToolName = "task"

// TaskRequest describes a delegated read-only investigation.
type TaskRequest struct {
	// Description is a short label for the subtask.
	Description string
	// Prompt is the complete, self-contained instruction for the subagent.
	Prompt string
}

// TaskRunner runs a delegated investigation in an isolated context and returns
// its final answer. The app layer implements it with a nested runtime that only
// has read-only tools.
type TaskRunner interface {
	RunTask(ctx context.Context, req TaskRequest) (string, error)
}

// TaskTool lets the main agent delegate a search or analysis task to a
// subagent, keeping the main conversation's context free of intermediate
// exploration output.
type TaskTool struct {
	runner       TaskRunner
	instructions string
}

// TaskOptions configures the task tool.
type TaskOptions struct {
	Runner          TaskRunner
	InstructionData InstructionData
}

func NewTaskTool(options TaskOptions) *TaskTool {
	return &TaskTool{runner: options.Runner}
}

func (t *TaskTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        TaskToolName,
		Description: "Delegate a read-only investigation to a subagent and get its findings back. Use it when locating something would take many exploratory steps.",
		Prompt: "The subagent only has read-only tools (read, glob, grep, ls) and cannot modify files or run commands. " +
			"Write a self-contained prompt: it does not see this conversation, so include the paths, symbols, and " +
			"questions it needs. Use it to locate code, summarize a subsystem, or answer a question that would " +
			"otherwise flood this context with search output.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"description": map[string]any{
					"type":        "string",
					"description": "Short (3-5 word) label for the subtask.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The complete, standalone task for the subagent.",
				},
			},
			"required": []string{"prompt"},
		},
		// A read-only subagent cannot change the workspace.
		Behavior: agent.ToolBehavior{ReadOnly: true},
	}
}

type taskArgs struct {
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
}

func (t *TaskTool) Run(ctx context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args taskArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, taskError("invalid args: " + err.Error())
	}
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return agent.ToolOutput{}, taskError("prompt is required")
	}
	if t.runner == nil {
		return agent.ToolOutput{}, taskError("no subagent runner is configured")
	}
	answer, err := t.runner.RunTask(ctx, TaskRequest{
		Description: strings.TrimSpace(args.Description),
		Prompt:      prompt,
	})
	if err != nil {
		return agent.ToolOutput{}, taskError(err.Error())
	}
	metadata := map[string]any{"description": args.Description}
	if answer == "" {
		answer = "(the subagent returned no findings)"
	}
	return agent.ToolOutput{Content: answer, Metadata: metadata}, nil
}

func taskError(message string) error {
	return fmt.Errorf("task: %s", message)
}
