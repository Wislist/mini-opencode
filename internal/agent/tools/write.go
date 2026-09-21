package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wislist/mini-opencode/internal/agent"
)

type WriteTool struct {
	options      FileOptions
	instructions string
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func NewWriteTool(options FileOptions) *WriteTool {
	options = normalizeFileOptions(options)
	instructions, _ := RenderToolInstructions(WriteToolName, options.InstructionData)
	return &WriteTool{options: options, instructions: instructions}
}

func (t *WriteTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        WriteToolName,
		Description: "Create or overwrite a workspace file.",
		Prompt:      t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
		Behavior: agent.ToolBehavior{Dangerous: true, RequiresConfirmation: true},
	}
}

func (t *WriteTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args writeArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("write: invalid args: %w", err)
	}
	path, err := resolveWorkspacePathWithOptions(t.options, args.Path)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("write: %w", err)
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("write: parent directory: %w", err)
	}
	if !info.IsDir() {
		return agent.ToolOutput{}, fmt.Errorf("write: parent is not a directory: %s", parent)
	}
	_, statErr := os.Stat(path)
	created := os.IsNotExist(statErr)
	if statErr != nil && !created {
		return agent.ToolOutput{}, fmt.Errorf("write: %w", statErr)
	}
	// Overwriting an existing file requires having read it first, and the
	// previous content is snapshotted so the session can restore it.
	var previous []byte
	if !created {
		if err := checkReadBeforeWrite(t.options, path, true); err != nil {
			return agent.ToolOutput{}, fmt.Errorf("write: %w", err)
		}
		if data, readErr := os.ReadFile(path); readErr == nil {
			previous = data
			observeSnapshot(t.options.Observer, path, data)
		}
	}
	if err := os.WriteFile(path, []byte(args.Content), 0644); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("write: %w", err)
	}
	metadata := map[string]any{
		"path":    path,
		"bytes":   len(args.Content),
		"created": created,
	}
	if previous != nil {
		metadata["previous_bytes"] = len(previous)
		metadata["snapshot"] = true
	}
	return agent.ToolOutput{
		Content:  fmt.Sprintf("wrote %s", path),
		Metadata: metadata,
	}, nil
}
