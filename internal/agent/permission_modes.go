package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// PermissionMode controls approval, not an OS sandbox. Safety and plan hooks
// run before this policy and remain authoritative in every mode.
type PermissionMode string

const (
	PermissionModeAsk        PermissionMode = "ask"
	PermissionModeAutoReview PermissionMode = "auto-review"
	PermissionModeFullAccess PermissionMode = "full-access"
)

func ParsePermissionMode(value string) (PermissionMode, error) {
	switch mode := PermissionMode(value); mode {
	case "", PermissionModeAsk:
		return PermissionModeAsk, nil
	case PermissionModeAutoReview, PermissionModeFullAccess:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown permission mode %q (use ask, auto-review, full-access)", value)
	}
}

func (m PermissionMode) Label() string {
	switch m {
	case PermissionModeAutoReview:
		return "帮我批准"
	case PermissionModeFullAccess:
		return "完全访问"
	default:
		return "请求批准"
	}
}

// ModePermissionPolicy owns the shared mode used by approval and tool-level
// path validation. The base policy is immutable after construction.
type ModePermissionPolicy struct {
	mu   sync.RWMutex
	mode PermissionMode
	base DefaultPermissionPolicy
}

func NewModePermissionPolicy(workDir string, roots []string, mode PermissionMode) (*ModePermissionPolicy, error) {
	p := &ModePermissionPolicy{base: NewDefaultPermissionPolicyWithRoots(workDir, append([]string(nil), roots...))}
	if err := p.SetMode(mode); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *ModePermissionPolicy) SetMode(mode PermissionMode) error {
	normalized, err := ParsePermissionMode(string(mode))
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.mode = normalized
	p.mu.Unlock()
	return nil
}

func (p *ModePermissionPolicy) Mode() PermissionMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.mode == "" {
		return PermissionModeAsk
	}
	return p.mode
}

func (p *ModePermissionPolicy) FullAccess() bool { return p.Mode() == PermissionModeFullAccess }

func (p *ModePermissionPolicy) Check(ctx context.Context, call ToolCall, def ToolDefinition) PermissionDecision {
	if err := ctx.Err(); err != nil {
		return PermissionDecision{Action: PermissionDeny, Reason: err.Error()}
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &args); err != nil || args == nil {
		return PermissionDecision{Action: PermissionDeny, Reason: "tool arguments must be a JSON object"}
	}
	mode := p.Mode()
	if mode == PermissionModeFullAccess {
		return PermissionDecision{Action: PermissionAllow, Reason: "full-access mode"}
	}
	decision := p.base.Check(ctx, call, def)
	if mode != PermissionModeAutoReview || decision.Action != PermissionConfirm {
		return decision
	}
	// This is deliberately a conservative local reviewer, not an LLM making
	// claims about arbitrary shell programs. Unknown tools still ask the user.
	if locallyReviewable(call, args) {
		return PermissionDecision{Action: PermissionAllow, Reason: "auto-review: bounded local operation"}
	}
	decision.Reason = "auto-review could not establish low risk; user approval required"
	return decision
}

func locallyReviewable(call ToolCall, args map[string]json.RawMessage) bool {
	switch call.Name {
	case "write", "edit":
		var path string
		return json.Unmarshal(args["path"], &path) == nil && strings.TrimSpace(path) != ""
	case "bash":
		var background bool
		if raw, ok := args["run_in_background"]; ok {
			if json.Unmarshal(raw, &background) != nil || background {
				return false
			}
		}
		var command string
		if json.Unmarshal(args["command"], &command) != nil {
			return false
		}
		// Exact commands only: no flags, expansions, pipelines, redirects,
		// project scripts or git commands (which may execute configured hooks).
		switch command {
		case "pwd", "/bin/pwd", "/bin/ls":
			return true
		}
	}
	return false
}

// PermissionCommand is shared by CLI and TUI. Merely viewing the choices or
// typing full-access without its explicit confirmation never changes mode.
func PermissionCommand(current PermissionMode, input string) (next PermissionMode, changed bool, message string) {
	if current == "" {
		current = PermissionModeAsk
	}
	fields := strings.Fields(input)
	if len(fields) == 1 {
		return current, false, fmt.Sprintf("permissions: %s · %s\n  /permissions auto-review  帮我批准（保守本地规则，不确定时询问）\n  /permissions full-access  完全访问\n  /permissions ask          请求批准\n仅本次进程生效；不是操作系统沙箱。", current, current.Label())
	}
	if len(fields) < 2 || len(fields) > 3 || (len(fields) == 3 && (fields[1] != "full-access" || fields[2] != "confirm")) {
		return current, false, "usage: /permissions [ask|auto-review|full-access [confirm]]"
	}
	mode, err := ParsePermissionMode(fields[1])
	if err != nil {
		return current, false, err.Error()
	}
	if mode == PermissionModeFullAccess && current != mode && len(fields) != 3 {
		return current, false, "警告：完全访问将跳过逐次审批并解除工具层工作区限制；可访问当前用户有权限的文件和网络。安全 Hook、plan 模式和先读后写仍有效。\n确认请输入 /permissions full-access confirm"
	}
	return mode, mode != current, fmt.Sprintf("permissions: %s · %s（仅本次进程；安全 Hook 仍有效）", mode, mode.Label())
}
