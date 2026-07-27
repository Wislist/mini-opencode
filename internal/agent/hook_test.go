package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSafetyHookDeniesDestructiveCommand(t *testing.T) {
	h := NewSafetyHook("/repo")
	cases := []string{
		`{"command":"git push --force origin main"}`,
		`{"command":"git reset --hard HEAD~1"}`,
		`{"command":"git clean -fdx"}`,
		`{"command":"drop database prod;"}`,
		`{"command":"rm -rf .git"}`,
		`{"command":"find . -delete"}`,
	}
	for _, args := range cases {
		d := h.BeforeToolCall(context.Background(), ToolCall{Name: "bash", Arguments: json.RawMessage(args)}, ToolDefinition{})
		if d.Action != HookDeny {
			t.Errorf("for %s: action = %q, want deny", args, d.Action)
		}
		if !strings.Contains(d.Reason, "safety") {
			t.Errorf("for %s: reason = %q", args, d.Reason)
		}
	}
}

func TestSafetyHookStopsCatastrophicCommand(t *testing.T) {
	h := NewSafetyHook("/repo")
	d := h.BeforeToolCall(context.Background(), ToolCall{
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"rm -rf /"}`),
	}, ToolDefinition{})
	if d.Action != HookDeny {
		t.Fatalf("action = %q, want deny (Stop handled at runtime)", d.Action)
	}
}

func TestSafetyHookAllowsSafeCommand(t *testing.T) {
	h := NewSafetyHook("/repo")
	d := h.BeforeToolCall(context.Background(), ToolCall{
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"go test ./..."}`),
	}, ToolDefinition{})
	if d.Action != HookContinue {
		t.Fatalf("action = %q, want continue", d.Action)
	}
}

func TestSafetyHookDeniesWorkspaceRootWrite(t *testing.T) {
	h := NewSafetyHook("/repo")
	d := h.BeforeToolCall(context.Background(), ToolCall{
		Name:      "write",
		Arguments: json.RawMessage(`{"path":"/repo"}`),
	}, ToolDefinition{})
	if d.Action != HookDeny {
		t.Fatalf("action = %q", d.Action)
	}
}

func TestSafetyHookDeniesGitDirWrite(t *testing.T) {
	h := NewSafetyHook("/repo")
	d := h.BeforeToolCall(context.Background(), ToolCall{
		Name:      "write",
		Arguments: json.RawMessage(`{"path":".git/config"}`),
	}, ToolDefinition{})
	if d.Action != HookDeny {
		t.Fatalf("action = %q", d.Action)
	}
	if !strings.Contains(d.Reason, ".git") {
		t.Fatalf("reason = %q", d.Reason)
	}
}

func TestSafetyHookAllowsRegularFile(t *testing.T) {
	h := NewSafetyHook("/repo")
	d := h.BeforeToolCall(context.Background(), ToolCall{
		Name:      "write",
		Arguments: json.RawMessage(`{"path":"src/main.go"}`),
	}, ToolDefinition{})
	if d.Action != HookContinue {
		t.Fatalf("action = %q", d.Action)
	}
}

func TestLoopGuardStopsRepeatedToolCalls(t *testing.T) {
	h := NewLoopGuardHook()
	h.OnRunStart(context.Background())
	call := ToolCall{
		ID:        "c1",
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"a.txt"}`),
	}
	for i := 1; i < 3; i++ {
		if d := h.BeforeToolCall(context.Background(), call, ToolDefinition{}); d.Action != HookContinue {
			t.Fatalf("turn %d: action = %q", i, d.Action)
		}
	}
	d := h.BeforeToolCall(context.Background(), call, ToolDefinition{})
	if d.Action != HookStop {
		t.Fatalf("action = %q, want stop", d.Action)
	}
	if !strings.Contains(d.Reason, "repeated") {
		t.Fatalf("reason = %q", d.Reason)
	}
}

func TestLoopGuardAllowsDistinctCalls(t *testing.T) {
	h := NewLoopGuardHook()
	h.OnRunStart(context.Background())
	calls := []ToolCall{
		{Name: "read", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
		{Name: "read", Arguments: json.RawMessage(`{"path":"b.txt"}`)},
		{Name: "read", Arguments: json.RawMessage(`{"path":"c.txt"}`)},
	}
	for _, c := range calls {
		if d := h.BeforeToolCall(context.Background(), c, ToolDefinition{}); d.Action != HookContinue {
			t.Fatalf("action = %q for %+v", d.Action, c)
		}
	}
}

func TestLoopGuardStopsRepeatedTurns(t *testing.T) {
	h := NewLoopGuardHook()
	h.OnRunStart(context.Background())
	history := []Message{{
		Role:      RoleAssistant,
		Content:   "Let me check.",
		ToolCalls: []ToolCall{{ID: "1", Name: "read", Arguments: json.RawMessage(`{"path":"a"}`)}},
	}}
	for i := 1; i < 3; i++ {
		if d := h.AfterTurn(context.Background(), i, history); d.Action != HookContinue {
			t.Fatalf("turn %d: action = %q", i, d.Action)
		}
	}
	d := h.AfterTurn(context.Background(), 3, history)
	if d.Action != HookStop {
		t.Fatalf("action = %q, want stop", d.Action)
	}
	if !strings.Contains(d.Reason, "identical") {
		t.Fatalf("reason = %q", d.Reason)
	}
}

func TestLoopGuardResetsOnRunStart(t *testing.T) {
	h := NewLoopGuardHook()
	h.OnRunStart(context.Background())
	call := ToolCall{Name: "read", Arguments: json.RawMessage(`{"path":"a"}`)}
	for i := 0; i < 3; i++ {
		h.BeforeToolCall(context.Background(), call, ToolDefinition{})
	}
	if d := h.BeforeToolCall(context.Background(), call, ToolDefinition{}); d.Action != HookStop {
		t.Fatalf("expected stop before reset, got %q", d.Action)
	}
	// Reset and the same call should be allowed again.
	h.OnRunStart(context.Background())
	if d := h.BeforeToolCall(context.Background(), call, ToolDefinition{}); d.Action != HookContinue {
		t.Fatalf("after reset: action = %q", d.Action)
	}
}

func TestHookChainFirstStopWins(t *testing.T) {
	var chain HookChain
	chain = append(chain, NewSafetyHook("/repo"))
	chain = append(chain, NewLoopGuardHook())
	chain.OnRunStart(context.Background())
	// A catastrophic command: SafetyHook denies it; chain returns deny.
	d := chain.BeforeToolCall(context.Background(), ToolCall{
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"drop database prod"}`),
	}, ToolDefinition{})
	if d.Action != HookDeny {
		t.Fatalf("action = %q, want deny", d.Action)
	}
}
