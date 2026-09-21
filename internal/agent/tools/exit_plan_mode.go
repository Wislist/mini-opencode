package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

const ExitPlanModeToolName = "exit_plan_mode"

// PlanApprover asks the user to approve a submitted plan. approved is false
// when the user rejects it; feedback carries an optional reason.
type PlanApprover interface {
	ApprovePlan(ctx context.Context, plan string) (approved bool, feedback string)
}

// ExitPlanModeTool closes the plan-mode loop: the agent submits its plan, the
// user approves or rejects it, and the tool result tells the model whether it
// may start implementing. On approval the host turns plan mode off.
type ExitPlanModeTool struct {
	approver PlanApprover
}

// ExitPlanModeOptions configures the plan submission tool.
type ExitPlanModeOptions struct {
	Approver PlanApprover
}

func NewExitPlanModeTool(options ExitPlanModeOptions) *ExitPlanModeTool {
	return &ExitPlanModeTool{approver: options.Approver}
}

func (t *ExitPlanModeTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        ExitPlanModeToolName,
		Description: "Present a completed plan to the user and ask to leave plan mode. Call this once the plan is ready; implementation starts only after the user approves.",
		Prompt: "Send the full plan as markdown: the files you will change, the approach, and how you will verify it. " +
			"If the user rejects the plan, revise it and call this tool again. Do not start editing before it is approved.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"plan": map[string]any{
					"type":        "string",
					"description": "The complete plan in markdown.",
				},
			},
			"required": []string{"plan"},
		},
		// Submitting a plan changes no files and must stay available in plan
		// mode, so it is marked read-only.
		Behavior: agent.ToolBehavior{ReadOnly: true},
	}
}

type exitPlanModeArgs struct {
	Plan string `json:"plan"`
}

func (t *ExitPlanModeTool) Run(ctx context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args exitPlanModeArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("exit_plan_mode: invalid args: %w", err)
	}
	plan := strings.TrimSpace(args.Plan)
	if plan == "" {
		return agent.ToolOutput{}, fmt.Errorf("exit_plan_mode: plan is required")
	}
	if t.approver == nil {
		return agent.ToolOutput{}, fmt.Errorf("exit_plan_mode: no approver is configured to review the plan")
	}

	approved, feedback := t.approver.ApprovePlan(ctx, plan)
	metadata := map[string]any{
		"plan":     plan,
		"approved": approved,
	}
	if feedback != "" {
		metadata["feedback"] = feedback
	}

	if approved {
		return agent.ToolOutput{
			Content:  "plan approved. Plan mode is now off: implement the plan and report progress against it.",
			Metadata: metadata,
		}, nil
	}
	content := "plan rejected by the user; you are still in plan mode. Revise the plan and submit it again."
	if feedback != "" {
		content += " Feedback: " + feedback
	}
	return agent.ToolOutput{Content: content, Metadata: metadata}, nil
}
