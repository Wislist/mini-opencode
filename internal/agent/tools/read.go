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
	// Record the read before slicing: even a partial read counts as having
	// observed the file, which is what unlocks a later write or edit.
	observeRead(t.options.Observer, path)

	if args.Offset < 1 {
		args.Offset = 1
	}
	if args.Limit <= 0 {
		args.Limit = DefaultReadLimit
	}
	if args.Limit > MaxReadLimit {
		args.Limit = MaxReadLimit
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

	// A line budget alone is not enough: minified or generated files pack
	// megabytes into a few lines. Stop at the byte budget as well so one read
	// can never blow up the context.
	maxChars := t.options.InstructionData.MaxOutputLength
	if maxChars <= 0 {
		maxChars = DefaultMaxOutputLength
	}
	var out strings.Builder
	written := 0
	truncatedBy := ""
	for i := start; i < end; i++ {
		line := fmt.Sprintf("%4d| %s\n", i+1, lines[i])
		remaining := maxChars - written
		if remaining <= 0 {
			end = i
			truncatedBy = "bytes"
			break
		}
		if len(line) > remaining {
			// A single oversized line is clipped rather than dropped: returning
			// nothing would be useless, returning all of it defeats the budget.
			out.WriteString(clipBytes(line, remaining))
			out.WriteString("\n")
			written += remaining
			end = i + 1
			truncatedBy = "bytes"
			break
		}
		out.WriteString(line)
		written += len(line)
	}
	if truncatedBy == "" && end < len(lines) {
		truncatedBy = "lines"
	}
	if truncatedBy != "" {
		fmt.Fprintf(&out, "… truncated (%s): showing lines %d-%d of %d; continue with offset=%d\n",
			truncatedBy, start+1, end, len(lines), end+1)
	}
	return agent.ToolOutput{
		Content: out.String(),
		Metadata: map[string]any{
			"path":         path,
			"offset":       args.Offset,
			"lines":        end - start,
			"total_lines":  len(lines),
			"truncated":    truncatedBy != "",
			"truncated_by": truncatedBy,
		},
	}, nil
}
