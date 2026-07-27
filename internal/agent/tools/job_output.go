package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wislist/mini-opencode/internal/agent"
)

type JobOutputTool struct {
	jobs            *JobManager
	maxOutputLength int
	instructions    string
}

type JobOutputOptions struct {
	Jobs            *JobManager
	MaxOutputLength int
	InstructionData InstructionData
}

type jobOutputArgs struct {
	JobID string `json:"job_id"`
}

func NewJobOutputTool(options JobOutputOptions) *JobOutputTool {
	if options.Jobs == nil {
		options.Jobs = NewJobManager()
	}
	if options.MaxOutputLength <= 0 {
		options.MaxOutputLength = DefaultMaxOutputLength
	}
	if options.InstructionData.MaxOutputLength == 0 {
		options.InstructionData = DefaultInstructionData()
	}
	instructions, _ := RenderToolInstructions(JobOutputToolName, options.InstructionData)
	return &JobOutputTool{
		jobs:            options.Jobs,
		maxOutputLength: options.MaxOutputLength,
		instructions:    instructions,
	}
}

func (t *JobOutputTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        JobOutputToolName,
		Description: "Read output and status from a background shell job.",
		Prompt:      t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"job_id": map[string]any{
					"type":        "string",
					"description": "Background shell job id returned by bash.",
				},
			},
			"required": []string{"job_id"},
		},
		Behavior: agent.ToolBehavior{ReadOnly: true},
	}
}

func (t *JobOutputTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args jobOutputArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("job_output: invalid args: %w", err)
	}
	if args.JobID == "" {
		return agent.ToolOutput{}, fmt.Errorf("job_output: job_id is required")
	}
	job, ok := t.jobs.Get(args.JobID)
	if !ok {
		return agent.ToolOutput{}, fmt.Errorf("job_output: job not found: %s", args.JobID)
	}

	snapshot := job.Snapshot()
	output, truncated := truncate(snapshot.Output, t.maxOutputLength)
	return agent.ToolOutput{
		Content: formatJobOutput(snapshot, output),
		Metadata: map[string]any{
			"job_id":    snapshot.ID,
			"cwd":       snapshot.CWD,
			"status":    string(snapshot.Status),
			"exit_code": snapshot.ExitCode,
			"truncated": truncated,
		},
	}, nil
}

func formatJobOutput(snapshot JobSnapshot, output string) string {
	return fmt.Sprintf("<job id=%q status=%q cwd=%q exit_code=%d>\n%s\n</job>",
		snapshot.ID,
		snapshot.Status,
		snapshot.CWD,
		snapshot.ExitCode,
		output,
	)
}
