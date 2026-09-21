package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

type fakePlanApprover struct {
	approved bool
	feedback string
	gotPlan  string
	calls    int
}

func (f *fakePlanApprover) ApprovePlan(_ context.Context, plan string) (bool, string) {
	f.calls++
	f.gotPlan = plan
	return f.approved, f.feedback
}

func TestExitPlanModeApprovedTellsModelToImplement(t *testing.T) {
	approver := &fakePlanApprover{approved: true}
	tool := NewExitPlanModeTool(ExitPlanModeOptions{Approver: approver})

	args, _ := json.Marshal(map[string]any{"plan": "1. add the tool\n2. test it"})
	out, err := tool.Run(context.Background(), agent.ToolInput{Arguments: args})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if approver.calls != 1 {
		t.Fatalf("approver calls = %d, want 1", approver.calls)
	}
	if approver.gotPlan != "1. add the tool\n2. test it" {
		t.Fatalf("plan passed to approver = %q", approver.gotPlan)
	}
	if approved, _ := out.Metadata["approved"].(bool); !approved {
		t.Fatalf("metadata = %#v, want approved", out.Metadata)
	}
	if !strings.Contains(out.Content, "approved") {
		t.Fatalf("content = %q", out.Content)
	}
}

func TestExitPlanModeRejectedKeepsPlanMode(t *testing.T) {
	approver := &fakePlanApprover{approved: false, feedback: "too broad"}
	tool := NewExitPlanModeTool(ExitPlanModeOptions{Approver: approver})

	args, _ := json.Marshal(map[string]any{"plan": "big plan"})
	out, err := tool.Run(context.Background(), agent.ToolInput{Arguments: args})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if approved, _ := out.Metadata["approved"].(bool); approved {
		t.Fatalf("metadata = %#v, want rejected", out.Metadata)
	}
	if !strings.Contains(out.Content, "still in plan mode") || !strings.Contains(out.Content, "too broad") {
		t.Fatalf("content = %q", out.Content)
	}
}

func TestExitPlanModeRejectsEmptyPlanAndMissingApprover(t *testing.T) {
	tool := NewExitPlanModeTool(ExitPlanModeOptions{Approver: &fakePlanApprover{approved: true}})
	empty, _ := json.Marshal(map[string]any{"plan": "   "})
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: empty}); err == nil {
		t.Fatal("Run() error = nil, want rejection of an empty plan")
	}

	noApprover := NewExitPlanModeTool(ExitPlanModeOptions{})
	args, _ := json.Marshal(map[string]any{"plan": "plan"})
	if _, err := noApprover.Run(context.Background(), agent.ToolInput{Arguments: args}); err == nil {
		t.Fatal("Run() error = nil, want missing-approver error")
	}
}
