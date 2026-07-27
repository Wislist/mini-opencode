package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

type LSTool struct {
	options      FileOptions
	instructions string
}

type lsArgs struct {
	Path string `json:"path"`
}

func NewLSTool(options FileOptions) *LSTool {
	options = normalizeFileOptions(options)
	instructions, _ := RenderToolInstructions(LSToolName, options.InstructionData)
	return &LSTool{options: options, instructions: instructions}
}

func (t *LSTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        LSToolName,
		Description: "List files and directories under a workspace path.",
		Prompt:      t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
		Behavior: agent.ToolBehavior{ReadOnly: true},
	}
}

func (t *LSTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args lsArgs
	if len(input.Arguments) > 0 {
		if err := json.Unmarshal(input.Arguments, &args); err != nil {
			return agent.ToolOutput{}, fmt.Errorf("ls: invalid args: %w", err)
		}
	}
	if args.Path == "" {
		args.Path = "."
	}
	path, err := resolveWorkspacePathWithOptions(t.options, args.Path)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("ls: %w", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("ls: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	var out strings.Builder
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return agent.ToolOutput{}, fmt.Errorf("ls: %w", err)
		}
		kind := "file"
		if entry.IsDir() {
			kind = "dir"
		}
		fmt.Fprintf(&out, "%s\t%d\t%s\t%s\n", kind, info.Size(), info.ModTime().Format("2006-01-02 15:04:05"), entry.Name())
	}
	return agent.ToolOutput{
		Content: out.String(),
		Metadata: map[string]any{
			"path":  path,
			"count": len(entries),
		},
	}, nil
}
