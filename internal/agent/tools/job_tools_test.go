package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

func TestJobOutputToolReadsCompletedJob(t *testing.T) {
	jobs := NewJobManager()
	job, err := jobs.Start("printf hello", t.TempDir())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-job.Done()

	tool := NewJobOutputTool(JobOutputOptions{Jobs: jobs})
	out, err := tool.Run(context.Background(), jobToolInput(JobOutputToolName, job.ID()))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "hello") {
		t.Fatalf("content = %q", out.Content)
	}
	if out.Metadata["status"] != string(JobExited) {
		t.Fatalf("status = %v", out.Metadata["status"])
	}
}

func TestJobKillToolKillsRunningJob(t *testing.T) {
	jobs := NewJobManager()
	job, err := jobs.Start("sleep 5", t.TempDir())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	tool := NewJobKillTool(JobKillOptions{Jobs: jobs})
	out, err := tool.Run(context.Background(), jobToolInput(JobKillToolName, job.ID()))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Metadata["status"] != string(JobKilled) {
		t.Fatalf("status = %v", out.Metadata["status"])
	}
}

func TestJobOutputToolRejectsMissingJob(t *testing.T) {
	tool := NewJobOutputTool(JobOutputOptions{Jobs: NewJobManager()})
	_, err := tool.Run(context.Background(), jobToolInput(JobOutputToolName, "missing"))
	if err == nil {
		t.Fatal("expected missing job error")
	}
}

func jobToolInput(name string, jobID string) agent.ToolInput {
	data, _ := json.Marshal(map[string]string{"job_id": jobID})
	return agent.ToolInput{
		CallID:    "call-1",
		Name:      name,
		Arguments: data,
	}
}
