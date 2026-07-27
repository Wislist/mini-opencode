package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wislist/mini-opencode/internal/agent"
)

type JobKillTool struct {
	jobs         *JobManager
	instructions string
}

type JobKillOptions struct {
	Jobs            *JobManager
	InstructionData InstructionData
}

type jobKillArgs struct {
	JobID string `json:"job_id"`
}

func NewJobKillTool(options JobKillOptions) *JobKillTool {
	if options.Jobs == nil {
		options.Jobs = NewJobManager()
	}
	if options.InstructionData.MaxOutputLength == 0 {
		options.InstructionData = DefaultInstructionData()
	}
	instructions, _ := RenderToolInstructions(JobKillToolName, options.InstructionData)
	return &JobKillTool{jobs: options.Jobs, instructions: instructions}
}

func (t *JobKillTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        JobKillToolName,
		Description: "Terminate a background shell job.",
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
		Behavior: agent.ToolBehavior{
			Dangerous:            true,
			RequiresConfirmation: true,
		},
	}
}

func (t *JobKillTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args jobKillArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("job_kill: invalid args: %w", err)
	}
	if args.JobID == "" {
		return agent.ToolOutput{}, fmt.Errorf("job_kill: job_id is required")
	}
	job, ok := t.jobs.Get(args.JobID)
	if !ok {
		return agent.ToolOutput{}, fmt.Errorf("job_kill: job not found: %s", args.JobID)
	}

	before := job.Snapshot()
	if before.Status == JobRunning {
		job.Kill()
	}
	after := job.Snapshot()
	return agent.ToolOutput{
		Content: fmt.Sprintf("job %s status: %s", after.ID, after.Status),
		Metadata: map[string]any{
			"job_id": after.ID,
			"cwd":    after.CWD,
			"status": string(after.Status),
		},
	}, nil
}
