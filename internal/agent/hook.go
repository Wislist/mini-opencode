package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// HookAction controls runtime flow after a hook fires.
type HookAction string

const (
	// HookContinue lets the runtime proceed normally.
	HookContinue HookAction = "continue"
	// HookDeny blocks a single tool call; the run continues with a recorded
	// denial result so the model can recover.
	HookDeny HookAction = "deny"
	// HookStop aborts the whole run immediately, for example when a
	// catastrophic action is attempted or the agent is stuck in a loop.
	HookStop HookAction = "stop"
)

// HookDecision is the verdict returned by a Hook check.
type HookDecision struct {
	Action HookAction
	Reason string
}

// Continue is the zero-effort "no opinion" decision.
func Continue() HookDecision { return HookDecision{Action: HookContinue} }

// Deny builds a deny decision with a reason.
func Deny(reason string) HookDecision { return HookDecision{Action: HookDeny, Reason: reason} }

// Stop builds a stop decision with a reason.
func Stop(reason string) HookDecision { return HookDecision{Action: HookStop, Reason: reason} }

// Hook intercepts the runtime lifecycle to guard against dangerous behavior
// (such as deleting the repository) and wasted tokens from repeated reasoning.
//
// OnRunStart is called once per Run invocation so stateful hooks can reset.
// BeforeToolCall fires before a tool is executed; returning Deny blocks that
// call, Stop aborts the run. AfterTurn fires once each turn after tool results
// are recorded; returning Stop aborts the run.
type Hook interface {
	OnRunStart(ctx context.Context)
	BeforeToolCall(ctx context.Context, call ToolCall, def ToolDefinition) HookDecision
	AfterTurn(ctx context.Context, turn int, history []Message) HookDecision
}

// HookChain runs hooks in order. The first non-continue decision wins; Stop
// always wins over Deny when both appear.
type HookChain []Hook

func (c HookChain) OnRunStart(ctx context.Context) {
	for _, h := range c {
		h.OnRunStart(ctx)
	}
}

func (c HookChain) BeforeToolCall(ctx context.Context, call ToolCall, def ToolDefinition) HookDecision {
	var deny *HookDecision
	for _, h := range c {
		d := h.BeforeToolCall(ctx, call, def)
		switch d.Action {
		case HookStop:
			return d
		case HookDeny:
			deny = &d
		}
	}
	if deny != nil {
		return *deny
	}
	return Continue()
}

func (c HookChain) AfterTurn(ctx context.Context, turn int, history []Message) HookDecision {
	for _, h := range c {
		d := h.AfterTurn(ctx, turn, history)
		if d.Action != HookContinue {
			return d
		}
	}
	return Continue()
}

// --- SafetyHook -----------------------------------------------------------

// SafetyHook is a danger guard. It blocks destructive shell commands (such as
// deleting the repository, dropping a database, or force-pushing) and aborts
// the run when a catastrophic, unrecoverable action is attempted.
type SafetyHook struct {
	WorkDir      string
	StopPatterns []string // matched case-insensitively against bash commands; abort run
	DenyPatterns []string // matched case-insensitively; block the call, continue run
}

// NewSafetyHook builds a SafetyHook with sensible defaults for the given
// workspace. The workspace root and its .git directory are protected.
func NewSafetyHook(workDir string) *SafetyHook {
	return &SafetyHook{
		WorkDir: workDir,
		StopPatterns: []string{
			"rm -rf /",
			"rm -fr /",
			"mkfs",
			":(){ :|:& };:",
			"dd if=/dev/zero",
			"dd if=/dev/urandom",
			"chmod -R 000",
		},
		DenyPatterns: []string{
			"drop database",
			"drop table",
			"truncate table",
			"git push --force",
			"git push -f",
			"git push --force-with-lease",
			"git clean -fd",
			"git clean -fdx",
			"git reset --hard",
			"find . -delete",
			"find . -type f -delete",
			"rm -rf .git",
			"rm -rf .",
			"rm -rf $pwd",
			"rm -rf ~",
			"shred -u",
		},
	}
}

func (h *SafetyHook) OnRunStart(_ context.Context) {}

func (h *SafetyHook) BeforeToolCall(_ context.Context, call ToolCall, def ToolDefinition) HookDecision {
	if call.Name == "bash" {
		if reason := h.checkCommand(call.Arguments); reason != "" {
			return Deny(reason)
		}
	}
	if reason := h.checkPath(call.Arguments); reason != "" {
		return Deny(reason)
	}
	_ = def
	return Continue()
}

func (h *SafetyHook) AfterTurn(_ context.Context, _ int, _ []Message) HookDecision {
	return Continue()
}

func (h *SafetyHook) checkCommand(args json.RawMessage) string {
	var values map[string]any
	if err := json.Unmarshal(args, &values); err != nil {
		return ""
	}
	command, _ := values["command"].(string)
	if command == "" {
		return ""
	}
	lower := strings.ToLower(command)
	for _, p := range h.StopPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return fmt.Sprintf("safety: catastrophic command blocked (%q); run aborted", p)
		}
	}
	for _, p := range h.DenyPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return fmt.Sprintf("safety: destructive command blocked: %q", p)
		}
	}
	return ""
}

// checkPath refuses writes/edits that target the .git directory or the
// workspace root itself, which would destroy the repository.
func (h *SafetyHook) checkPath(args json.RawMessage) string {
	if h.WorkDir == "" {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal(args, &values); err != nil {
		return ""
	}
	for _, key := range []string{"path", "working_dir"} {
		raw, ok := values[key].(string)
		if !ok || raw == "" {
			continue
		}
		target := raw
		if !isAbs(target) {
			target = joinPath(h.WorkDir, target)
		}
		abs := cleanPath(target)
		base := cleanPath(h.WorkDir)
		if abs == base {
			return fmt.Sprintf("safety: refusing to overwrite/delete workspace root: %s", raw)
		}
		if strings.HasPrefix(abs, base+sep()+".git") {
			return fmt.Sprintf("safety: refusing to modify .git: %s", raw)
		}
	}
	return ""
}

// --- LoopGuardHook --------------------------------------------------------

// LoopGuardHook detects wasted tokens from repeated reasoning. It stops the
// run when the agent repeats the same tool call too many times, or when entire
// turns (assistant text + tool calls) repeat identically, indicating the
// agent is stuck in a loop.
type LoopGuardHook struct {
	// MaxRepeats is the number of identical repeats that triggers a stop.
	// The default of 3 means the 3rd identical repeat aborts the run.
	MaxRepeats int

	callCounts  map[string]int
	lastTurnSig string
	repeatTurns int
}

// NewLoopGuardHook builds a LoopGuardHook with MaxRepeats defaulting to 3.
func NewLoopGuardHook() *LoopGuardHook {
	return &LoopGuardHook{MaxRepeats: 3}
}

func (h *LoopGuardHook) OnRunStart(_ context.Context) {
	if h.MaxRepeats <= 0 {
		h.MaxRepeats = 3
	}
	h.callCounts = map[string]int{}
	h.lastTurnSig = ""
	h.repeatTurns = 0
}

func (h *LoopGuardHook) BeforeToolCall(_ context.Context, call ToolCall, _ ToolDefinition) HookDecision {
	if h.callCounts == nil {
		h.OnRunStart(context.Background())
	}
	sig := callSignature(call)
	h.callCounts[sig]++
	if h.callCounts[sig] >= h.MaxRepeats {
		return Stop(fmt.Sprintf(
			"loop guard: tool %q repeated %d times with identical arguments; stopping to avoid wasting tokens",
			call.Name, h.callCounts[sig]))
	}
	return Continue()
}

func (h *LoopGuardHook) AfterTurn(_ context.Context, _ int, history []Message) HookDecision {
	if h.callCounts == nil {
		h.OnRunStart(context.Background())
	}
	sig := turnSignature(history)
	if sig == "" {
		return Continue()
	}
	if sig == h.lastTurnSig {
		h.repeatTurns++
	} else {
		h.repeatTurns = 1
		h.lastTurnSig = sig
	}
	if h.repeatTurns >= h.MaxRepeats {
		return Stop(fmt.Sprintf(
			"loop guard: identical agent response repeated %d turns; stopping to avoid wasting tokens",
			h.repeatTurns))
	}
	return Continue()
}

// callSignature is the stable identity of a tool call for repeat detection.
func callSignature(call ToolCall) string {
	// Normalize whitespace in arguments so trivial reformatting does not hide
	// an otherwise identical call.
	var norm any
	if err := json.Unmarshal(call.Arguments, &norm); err != nil {
		return call.Name + ":" + string(call.Arguments)
	}
	canonical, err := json.Marshal(norm)
	if err != nil {
		return call.Name + ":" + string(call.Arguments)
	}
	return call.Name + ":" + string(canonical)
}

// turnSignature summarizes the last assistant message plus its tool calls so
// repeated turns can be detected.
func turnSignature(history []Message) string {
	if len(history) == 0 {
		return ""
	}
	var last *Message
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == RoleAssistant {
			last = &history[i]
			break
		}
	}
	if last == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(last.Content)
	b.WriteByte('|')
	for _, c := range last.ToolCalls {
		b.WriteString(callSignature(c))
		b.WriteByte(';')
	}
	return b.String()
}

// Path helpers kept local to avoid importing filepath in the hook's hot path
// tests; they mirror filepath behavior.
func isAbs(p string) bool {
	return strings.HasPrefix(p, "/")
}

func joinPath(base, rel string) string {
	if base == "" {
		return rel
	}
	return strings.TrimRight(base, "/") + "/" + rel
}

func cleanPath(p string) string {
	parts := strings.Split(p, "/")
	var stack []string
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			stack = append(stack, part)
		}
	}
	return "/" + strings.Join(stack, "/")
}

func sep() string { return "/" }
