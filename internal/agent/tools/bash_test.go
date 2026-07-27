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

	out, err := tool.Run(context.Background(), bashInput(`{"command":"printf hello"}`))
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

	_, err := tool.Run(context.Background(), bashInput(`{"command":"git push origin main"}`))
	if err == nil {
		t.Fatal("expected banned command error")
	}
}

func TestBashToolRejectsTrailingAmpersand(t *testing.T) {
	tool := NewBashTool(BashOptions{WorkDir: t.TempDir()})

	_, err := tool.Run(context.Background(), bashInput(`{"command":"sleep 10 &"}`))
	if err == nil {
		t.Fatal("expected trailing ampersand error")
	}
}

func TestBashToolRunsInBackground(t *testing.T) {
	tool := NewBashTool(BashOptions{WorkDir: t.TempDir()})

	out, err := tool.Run(context.Background(), bashInput(`{"command":"sleep 1; printf done","run_in_background":true}`))
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

	out, err := tool.Run(context.Background(), bashInput(`{"command":"sleep 1; printf done"}`))
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

func bashInput(raw string) agent.ToolInput {
	return agent.ToolInput{
		CallID:    "call-1",
		Name:      BashToolName,
		Arguments: json.RawMessage(raw),
	}
}
