package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

type ReadTool struct {
	options      FileOptions
	instructions string
}

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func NewReadTool(options FileOptions) *ReadTool {
	options = normalizeFileOptions(options)
	instructions, _ := RenderToolInstructions(ReadToolName, options.InstructionData)
	return &ReadTool{options: options, instructions: instructions}
}

func (t *ReadTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        ReadToolName,
		Description: "Read a workspace file with optional line range limits.",
		Prompt:      t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
				"offset": map[string]any{"type": "integer", "description": "1-based starting line"},
				"limit":  map[string]any{"type": "integer", "description": "maximum lines to return"},
			},
			"required": []string{"path"},
		},
		Behavior: agent.ToolBehavior{ReadOnly: true},
	}
}

func (t *ReadTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args readArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("read: invalid args: %w", err)
	}
	path, err := resolveWorkspacePathWithOptions(t.options, args.Path)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("read: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("read: %w", err)
	}
	if isLikelyBinary(data) {
		return agent.ToolOutput{}, fmt.Errorf("read: refusing to read binary file: %s", path)
	}

	if args.Offset < 1 {
		args.Offset = 1
	}
	if args.Limit <= 0 {
		args.Limit = 200
	}
	if args.Limit > 2000 {
		args.Limit = 2000
	}

	lines := strings.Split(string(data), "\n")
	start := args.Offset - 1
	if start > len(lines) {
		start = len(lines)
	}
	end := start + args.Limit
	if end > len(lines) {
		end = len(lines)
	}

	var out strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&out, "%4d| %s\n", i+1, lines[i])
	}
	return agent.ToolOutput{
		Content: out.String(),
		Metadata: map[string]any{
			"path":        path,
			"offset":      args.Offset,
			"lines":       end - start,
			"total_lines": len(lines),
		},
	}, nil
}
