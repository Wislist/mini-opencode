package agent

import (
	"context"
	"testing"
)

func TestPlanModeHookInactiveAllowsAll(t *testing.T) {
	h := &PlanModeHook{Active: false}
	call := ToolCall{Name: "write"}
	def := ToolDefinition{Name: "write", Behavior: ToolBehavior{ReadOnly: false}}
	if d := h.BeforeToolCall(context.Background(), call, def); d.Action != HookContinue {
		t.Fatalf("inactive hook should continue, got %s: %s", d.Action, d.Reason)
	}
}

func TestPlanModeHookActiveAllowsReadOnly(t *testing.T) {
	h := &PlanModeHook{Active: true}
	call := ToolCall{Name: "read"}
	def := ToolDefinition{Name: "read", Behavior: ToolBehavior{ReadOnly: true}}
	if d := h.BeforeToolCall(context.Background(), call, def); d.Action != HookContinue {
		t.Fatalf("read-only tool should be allowed, got %s: %s", d.Action, d.Reason)
	}
}

func TestPlanModeHookActiveDeniesWriteTools(t *testing.T) {
	h := &PlanModeHook{Active: true}
	call := ToolCall{Name: "write"}
	def := ToolDefinition{Name: "write", Behavior: ToolBehavior{ReadOnly: false}}
	d := h.BeforeToolCall(context.Background(), call, def)
	if d.Action != HookDeny {
		t.Fatalf("write tool should be denied, got %s", d.Action)
	}
	if d.Reason == "" {
		t.Fatal("denial should include a reason")
	}
}

func TestPlanModeHookActiveDeniesBash(t *testing.T) {
	h := &PlanModeHook{Active: true}
	call := ToolCall{Name: "bash"}
	def := ToolDefinition{Name: "bash", Behavior: ToolBehavior{ReadOnly: false, Dangerous: true}}
	d := h.BeforeToolCall(context.Background(), call, def)
	if d.Action != HookDeny {
		t.Fatalf("bash should be denied in plan mode, got %s", d.Action)
	}
}
