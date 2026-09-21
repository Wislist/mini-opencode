package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

type GrepTool struct {
	options      FileOptions
	instructions string
	maxResults   int
}

const (
	// maxGrepFileSize skips files large enough that scanning them would stall
	// the search without a matching payoff; minified bundles and data dumps
	// rarely help an agent reason about source.
	maxGrepFileSize = 4 << 20
	// maxGrepLineLength bounds a single scanned line so a file without
	// newlines cannot blow up memory.
	maxGrepLineLength = 1 << 20
	// binarySniffSize is how many leading bytes are inspected for NUL bytes.
	binarySniffSize = 8000
)

type grepArgs struct {
	Query      string `json:"query"`
	Path       string `json:"path"`
	MaxResults int    `json:"max_results"`
}

func NewGrepTool(options FileOptions) *GrepTool {
	options = normalizeFileOptions(options)
	instructions, _ := RenderToolInstructions(GrepToolName, options.InstructionData)
	return &GrepTool{options: options, instructions: instructions, maxResults: options.InstructionData.MaxResults}
}

func (t *GrepTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        GrepToolName,
		Description: "Search text in workspace files.",
		Prompt:      t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":       map[string]any{"type": "string"},
				"path":        map[string]any{"type": "string"},
				"max_results": map[string]any{"type": "integer"},
			},
			"required": []string{"query"},
		},
		Behavior: agent.ToolBehavior{ReadOnly: true},
	}
}

func (t *GrepTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args grepArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("grep: invalid args: %w", err)
	}
	if args.Query == "" {
		return agent.ToolOutput{}, fmt.Errorf("grep: query is required")
	}
	root := t.options.WorkDir
	if args.Path != "" {
		var err error
		root, err = resolveWorkspacePathWithOptions(t.options, args.Path)
		if err != nil {
			return agent.ToolOutput{}, fmt.Errorf("grep: %w", err)
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
	truncated := false
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
		if shouldSkipSearchPath(path) {
			return nil
		}
		fileMatches, err := grepFile(path, args.Query, maxResults-len(matches))
		if err != nil {
			return err
		}
		matches = append(matches, fileMatches...)
		if len(matches) >= maxResults {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("grep: %w", err)
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

func grepFile(path string, query string, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxGrepFileSize {
		return nil, nil
	}

	reader := bufio.NewReaderSize(file, 64*1024)
	// Sniff the leading bytes for NUL to skip binary files without reading the
	// whole file into memory; Peek keeps them for the scanner below. Peek
	// returns fewer bytes than requested at EOF, which is fine — the returned
	// slice is still the file's prefix.
	if head, err := reader.Peek(binarySniffSize); len(head) > 0 && (err == nil || err == io.EOF) {
		if isLikelyBinary(head) {
			return nil, nil
		}
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxGrepLineLength)
	var matches []string
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.Contains(line, query) {
			matches = append(matches, fmt.Sprintf("%s:%d:%s", path, lineNo, line))
			if len(matches) >= limit {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}
