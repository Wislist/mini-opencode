package agent

import "context"

// PlanModeHook enforces plan mode: when Active, it denies every tool call
// whose definition is not read-only. This lets the agent analyze and propose
// plans without modifying files or running commands. Read-only tools (read,
// grep, glob, ls) remain available so the agent can inspect the codebase.
type PlanModeHook struct {
	// Active gates enforcement. When false the hook is a no-op.
	Active bool
}

func (h *PlanModeHook) OnRunStart(_ context.Context) {}

func (h *PlanModeHook) BeforeToolCall(_ context.Context, call ToolCall, def ToolDefinition) HookDecision {
	if !h.Active {
		return Continue()
	}
	if def.Behavior.ReadOnly {
		return Continue()
	}
	return Deny("plan mode: tool \"" + call.Name + "\" is not read-only; code modifications are blocked in plan mode. provide a plan and suggestions instead.")
}

func (h *PlanModeHook) AfterTurn(_ context.Context, _ int, _ []Message) HookDecision {
	return Continue()
}
