package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

type EditTool struct {
	options      FileOptions
	instructions string
}

type editArgs struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

func NewEditTool(options FileOptions) *EditTool {
	options = normalizeFileOptions(options)
	instructions, _ := RenderToolInstructions(EditToolName, options.InstructionData)
	return &EditTool{options: options, instructions: instructions}
}

func (t *EditTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        EditToolName,
		Description: "Edit an existing workspace file by exact string replacement.",
		Prompt:      t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string"},
				"old_string": map[string]any{"type": "string"},
				"new_string": map[string]any{"type": "string"},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
		Behavior: agent.ToolBehavior{Dangerous: true, RequiresConfirmation: true},
	}
}

func (t *EditTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args editArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("edit: invalid args: %w", err)
	}
	if args.OldString == "" {
		return agent.ToolOutput{}, fmt.Errorf("edit: old_string is required")
	}
	path, err := resolveWorkspacePathWithOptions(t.options, args.Path)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("edit: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("edit: %w", err)
	}
	if isLikelyBinary(data) {
		return agent.ToolOutput{}, fmt.Errorf("edit: refusing to edit binary file: %s", path)
	}
	content := string(data)
	count := strings.Count(content, args.OldString)
	if count == 0 {
		return agent.ToolOutput{}, fmt.Errorf("edit: old_string not found")
	}
	if count > 1 {
		return agent.ToolOutput{}, fmt.Errorf("edit: old_string appears %d times", count)
	}
	updated := strings.Replace(content, args.OldString, args.NewString, 1)
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("edit: %w", err)
	}
	return agent.ToolOutput{
		Content: fmt.Sprintf("edited %s", path),
		Metadata: map[string]any{
			"path":         path,
			"replacements": 1,
		},
	}, nil
}
