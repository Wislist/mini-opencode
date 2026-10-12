package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"
)

func TestBashToolRunsForegroundCommand(t *testing.T) {
	tool := NewBashTool(BashOptions{WorkDir: t.TempDir()})

	out, err := tool.Run(context.Background(), bashInput(testPrintCommand("hello"), false))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "hello") {
		t.Fatalf("content = %q", out.Content)
	}
	if out.Metadata["status"] != string(JobExited) {
		t.Fatalf("status = %v", out.Metadata["status"])
	}
	if out.Metadata["exit_code"] != 0 {
		t.Fatalf("exit_code = %v", out.Metadata["exit_code"])
	}
}

func TestBashToolRejectsBannedCommand(t *testing.T) {
	tool := NewBashTool(BashOptions{
		WorkDir:        t.TempDir(),
		BannedCommands: []string{"git push"},
	})

	_, err := tool.Run(context.Background(), bashInput("git push origin main", false))
	if err == nil {
		t.Fatal("expected banned command error")
	}
}

func TestBashToolRejectsTrailingAmpersand(t *testing.T) {
	tool := NewBashTool(BashOptions{WorkDir: t.TempDir()})

	_, err := tool.Run(context.Background(), bashInput("sleep 10 &", false))
	if err == nil {
		t.Fatal("expected trailing ampersand error")
	}
}

func TestBashToolRunsInBackground(t *testing.T) {
	tool := NewBashTool(BashOptions{WorkDir: t.TempDir()})

	out, err := tool.Run(context.Background(), bashInput(testSleepThenPrintCommand(1, "done"), true))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	jobID, ok := out.Metadata["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatalf("missing job_id metadata: %#v", out.Metadata)
	}
	job, ok := tool.Jobs().Get(jobID)
	if !ok {
		t.Fatalf("job %s not found", jobID)
	}
	job.Kill()
}

func TestBashToolAutoBackgroundsLongCommand(t *testing.T) {
	tool := NewBashTool(BashOptions{
		WorkDir:             t.TempDir(),
		AutoBackgroundAfter: 20 * time.Millisecond,
	})

	out, err := tool.Run(context.Background(), bashInput(testSleepThenPrintCommand(1, "done"), false))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Metadata["status"] != "auto_backgrounded" {
		t.Fatalf("status = %v", out.Metadata["status"])
	}
	jobID := out.Metadata["job_id"].(string)
	job, ok := tool.Jobs().Get(jobID)
	if !ok {
		t.Fatalf("job %s not found", jobID)
	}
	job.Kill()
}

func bashInput(command string, runInBackground bool) agent.ToolInput {
	data, _ := json.Marshal(map[string]any{
		"command":           command,
		"run_in_background": runInBackground,
	})
	return agent.ToolInput{
		CallID:    "call-1",
		Name:      BashToolName,
		Arguments: data,
	}
}
