package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

type GlobTool struct {
	options      FileOptions
	instructions string
	maxResults   int
}

type globArgs struct {
	Pattern    string `json:"pattern"`
	WorkingDir string `json:"working_dir"`
	MaxResults int    `json:"max_results"`
}

func NewGlobTool(options FileOptions) *GlobTool {
	options = normalizeFileOptions(options)
	instructions, _ := RenderToolInstructions(GlobToolName, options.InstructionData)
	return &GlobTool{options: options, instructions: instructions, maxResults: options.InstructionData.MaxResults}
}

func (t *GlobTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        GlobToolName,
		Description: "Find workspace files by glob pattern.",
		Prompt:      t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string"},
				"working_dir": map[string]any{"type": "string"},
				"max_results": map[string]any{"type": "integer"},
			},
			"required": []string{"pattern"},
		},
		Behavior: agent.ToolBehavior{ReadOnly: true},
	}
}

func (t *GlobTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args globArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("glob: invalid args: %w", err)
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return agent.ToolOutput{}, fmt.Errorf("glob: pattern is required")
	}
	root := t.options.WorkDir
	if args.WorkingDir != "" {
		var err error
		root, err = resolveWorkspacePathWithOptions(t.options, args.WorkingDir)
		if err != nil {
			return agent.ToolOutput{}, fmt.Errorf("glob: %w", err)
		}
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = t.maxResults
	}
	if maxResults <= 0 {
		maxResults = 200
	}

	var matches []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && shouldSkipSearchPath(path) && path != root {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if shouldSkipSearchPath(rel) {
			return nil
		}
		ok, err := filepath.Match(args.Pattern, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if ok {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("glob: %w", err)
	}
	sort.Strings(matches)
	truncated := false
	if len(matches) > maxResults {
		matches = matches[:maxResults]
		truncated = true
	}

	return agent.ToolOutput{
		Content: strings.Join(matches, "\n"),
		Metadata: map[string]any{
			"root":      root,
			"count":     len(matches),
			"truncated": truncated,
		},
	}, nil
}
