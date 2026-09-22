package agent

import (
	"context"
	"sync"
)

// PlanModeHook enforces plan mode: when active, it denies every tool call
// whose definition is not read-only. This lets the agent analyze and propose
// plans without modifying files or running commands. Read-only tools (read,
// grep, glob, ls) remain available so the agent can inspect the codebase.
//
// The plan submission tool is exempt: it is how the agent hands a plan to the
// user for approval and, on approval, how plan mode ends. Without it plan mode
// would have no exit other than the user toggling the mode by hand.
//
// The mode is toggled from the UI goroutine (a key press) while the run
// goroutine reads it before every tool call, so the flag is guarded rather than
// exposed as a plain field.
type PlanModeHook struct {
	mu     sync.RWMutex
	active bool
	// AllowPlanTools lists non-read-only tool names that stay callable in plan
	// mode. Defaults to the plan submission tool and the todo list.
	AllowPlanTools []string
}

// DefaultPlanModeAllowedTools are the tools that remain callable in plan mode.
var DefaultPlanModeAllowedTools = []string{"exit_plan_mode", "todo_write", "todo_blocked"}

// NewPlanModeHook builds a plan mode hook in the given state.
func NewPlanModeHook(active bool) *PlanModeHook {
	return &PlanModeHook{active: active}
}

// SetActive turns plan mode on or off.
func (h *PlanModeHook) SetActive(active bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active = active
}

// IsActive reports whether plan mode is currently enforced.
func (h *PlanModeHook) IsActive() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.active
}

// BlocksTodoContinuation implements TodoChainGate: the task-list chain must not
// drive itself forward while plan mode forbids the work it would do.
func (h *PlanModeHook) BlocksTodoContinuation() bool { return h.IsActive() }

func (h *PlanModeHook) OnRunStart(_ context.Context) {}

func (h *PlanModeHook) BeforeToolCall(_ context.Context, call ToolCall, def ToolDefinition) HookDecision {
	if !h.IsActive() {
		return Continue()
	}
	if def.Behavior.ReadOnly || h.allowedInPlanMode(call.Name) {
		return Continue()
	}
	return Deny("plan mode: tool \"" + call.Name + "\" is not read-only; code modifications are blocked in plan mode. provide a plan and suggestions instead.")
}

func (h *PlanModeHook) AfterTurn(_ context.Context, _ int, _ []Message) HookDecision {
	return Continue()
}

func (h *PlanModeHook) allowedInPlanMode(name string) bool {
	allowed := h.AllowPlanTools
	if allowed == nil {
		allowed = DefaultPlanModeAllowedTools
	}
	for _, candidate := range allowed {
		if candidate == name {
			return true
		}
	}
	return false
}
