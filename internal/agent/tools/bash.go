package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"
)

type BashTool struct {
	options      BashOptions
	jobs         *JobManager
	instructions string
}

type BashOptions struct {
	WorkDir             string
	BannedCommands      []string
	MaxOutputLength     int
	AutoBackgroundAfter time.Duration
	InstructionData     InstructionData
	Jobs                *JobManager
}

type bashArgs struct {
	Command         string `json:"command"`
	WorkingDir      string `json:"working_dir"`
	RunInBackground bool   `json:"run_in_background"`
}

func NewBashTool(options BashOptions) *BashTool {
	if options.WorkDir == "" {
		options.WorkDir, _ = os.Getwd()
	}
	if len(options.BannedCommands) == 0 {
		options.BannedCommands = append([]string(nil), DefaultBannedCommands...)
	}
	if options.MaxOutputLength <= 0 {
		options.MaxOutputLength = DefaultMaxOutputLength
	}
	if options.AutoBackgroundAfter <= 0 {
		options.AutoBackgroundAfter = DefaultAutoBackgroundAfter
	}
	if options.Jobs == nil {
		options.Jobs = NewJobManager()
	}
	if options.InstructionData.MaxOutputLength == 0 {
		options.InstructionData = DefaultInstructionData()
	}

	instructions, _ := RenderToolInstructions(BashToolName, options.InstructionData)
	return &BashTool{options: options, jobs: options.Jobs, instructions: instructions}
}

func (t *BashTool) Jobs() *JobManager { return t.jobs }

func (t *BashTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        BashToolName,
		Description: "Execute shell commands; long-running commands can move to background.",
		Prompt:      t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute.",
				},
				"working_dir": map[string]any{
					"type":        "string",
					"description": "Optional working directory. Defaults to the agent working directory.",
				},
				"run_in_background": map[string]any{
					"type":        "boolean",
					"description": "Run command as a background job and return immediately.",
				},
			},
			"required": []string{"command"},
		},
		Behavior: agent.ToolBehavior{
			Dangerous:            true,
			RequiresConfirmation: true,
			SupportsBackground:   true,
		},
	}
}

func (t *BashTool) Run(ctx context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args bashArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("bash: invalid args: %w", err)
	}
	args.Command = strings.TrimSpace(args.Command)
	if args.Command == "" {
		return agent.ToolOutput{}, fmt.Errorf("bash: command is required")
	}
	if strings.HasSuffix(args.Command, "&") {
		return agent.ToolOutput{}, fmt.Errorf("bash: use run_in_background instead of trailing &")
	}
	if banned := t.bannedCommand(args.Command); banned != "" {
		return agent.ToolOutput{}, fmt.Errorf("bash: banned command matched %q", banned)
	}

	cwd, err := t.resolveWorkingDir(args.WorkingDir)
	if err != nil {
		return agent.ToolOutput{}, err
	}

	job, err := t.jobs.Start(args.Command, cwd)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("bash: start command: %w", err)
	}

	if args.RunInBackground {
		return t.backgroundOutput(job, "backgrounded"), nil
	}

	select {
	case <-ctx.Done():
		job.Kill()
		return agent.ToolOutput{}, ctx.Err()
	case <-job.Done():
		return t.finishedOutput(job), nil
	case <-time.After(t.options.AutoBackgroundAfter):
		return t.backgroundOutput(job, "auto_backgrounded"), nil
	}
}

func (t *BashTool) resolveWorkingDir(workingDir string) (string, error) {
	cwd := t.options.WorkDir
	if workingDir != "" {
		cwd = workingDir
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(t.options.WorkDir, cwd)
		}
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("bash: working_dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("bash: working_dir is not a directory: %s", cwd)
	}
	return cwd, nil
}

func (t *BashTool) bannedCommand(command string) string {
	normalized := strings.ToLower(command)
	for _, banned := range t.options.BannedCommands {
		banned = strings.TrimSpace(banned)
		if banned == "" {
			continue
		}
		if strings.Contains(normalized, strings.ToLower(banned)) {
			return banned
		}
	}
	return ""
}

func (t *BashTool) finishedOutput(job *Job) agent.ToolOutput {
	snapshot := job.Snapshot()
	output, truncated := truncate(snapshot.Output, t.options.MaxOutputLength)
	return agent.ToolOutput{
		Content: formatBashContent(snapshot.CWD, output),
		Metadata: map[string]any{
			"job_id":    snapshot.ID,
			"cwd":       snapshot.CWD,
			"status":    string(snapshot.Status),
			"exit_code": snapshot.ExitCode,
			"truncated": truncated,
		},
	}
}

func (t *BashTool) backgroundOutput(job *Job, status string) agent.ToolOutput {
	snapshot := job.Snapshot()
	output, truncated := truncate(snapshot.Output, t.options.MaxOutputLength)
	return agent.ToolOutput{
		Content: formatBashContent(snapshot.CWD, output),
		Metadata: map[string]any{
			"job_id":    snapshot.ID,
			"cwd":       snapshot.CWD,
			"status":    status,
			"truncated": truncated,
		},
	}
}

func formatBashContent(cwd string, output string) string {
	if strings.TrimSpace(output) == "" {
		return fmt.Sprintf("<cwd>%s</cwd>\n", cwd)
	}
	return fmt.Sprintf("<cwd>%s</cwd>\n%s", cwd, output)
}

func truncate(output string, maxLength int) (string, bool) {
	if maxLength <= 0 || len(output) <= maxLength {
		return output, false
	}
	return output[:maxLength] + "\n[truncated]\n", true
}
