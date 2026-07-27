package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type PermissionAction string

const (
	PermissionAllow   PermissionAction = "allow"
	PermissionDeny    PermissionAction = "deny"
	PermissionConfirm PermissionAction = "confirm"
)

type PermissionDecision struct {
	Action PermissionAction
	Reason string
}

type PermissionPolicy interface {
	Check(ctx context.Context, call ToolCall, definition ToolDefinition) PermissionDecision
}

type DefaultPermissionPolicy struct {
	WorkDir        string
	AllowedRoots   []string
	BannedCommands []string
}

func NewDefaultPermissionPolicy(workDir string) DefaultPermissionPolicy {
	return DefaultPermissionPolicy{
		WorkDir: workDir,
		BannedCommands: []string{
			"rm -rf",
			"sudo",
			"chmod -R",
			"chown -R",
			"git push",
			"git reset --hard",
		},
	}
}

// NewDefaultPermissionPolicyWithRoots builds a policy whose filesystem
// boundary is the working directory plus any additional allowed roots.
// Allowed roots are absolute, cleaned paths (config.Load normalizes them).
func NewDefaultPermissionPolicyWithRoots(workDir string, allowedRoots []string) DefaultPermissionPolicy {
	policy := NewDefaultPermissionPolicy(workDir)
	policy.AllowedRoots = allowedRoots
	return policy
}

func (p DefaultPermissionPolicy) Check(ctx context.Context, call ToolCall, definition ToolDefinition) PermissionDecision {
	select {
	case <-ctx.Done():
		return PermissionDecision{Action: PermissionDeny, Reason: ctx.Err().Error()}
	default:
	}

	if reason := p.checkWorkspacePaths(call.Arguments); reason != "" {
		return PermissionDecision{Action: PermissionDeny, Reason: reason}
	}
	if reason := p.checkBannedCommand(call.Arguments); reason != "" {
		return PermissionDecision{Action: PermissionDeny, Reason: reason}
	}
	if definition.Behavior.RequiresConfirmation || definition.Behavior.Dangerous {
		return PermissionDecision{Action: PermissionConfirm, Reason: "tool requires confirmation"}
	}
	return PermissionDecision{Action: PermissionAllow}
}

func (p DefaultPermissionPolicy) checkBannedCommand(args json.RawMessage) string {
	var values map[string]any
	if err := json.Unmarshal(args, &values); err != nil {
		return ""
	}
	command, ok := values["command"].(string)
	if !ok || command == "" {
		return ""
	}
	lower := strings.ToLower(command)
	for _, banned := range p.BannedCommands {
		if strings.Contains(lower, strings.ToLower(banned)) {
			return fmt.Sprintf("banned command matched %q", banned)
		}
	}
	return ""
}

func (p DefaultPermissionPolicy) checkWorkspacePaths(args json.RawMessage) string {
	if p.WorkDir == "" {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal(args, &values); err != nil {
		return ""
	}
	for _, key := range []string{"path", "working_dir"} {
		value, ok := values[key].(string)
		if !ok || value == "" {
			continue
		}
		if !pathWithinAnyWorkspace(p.WorkDir, p.AllowedRoots, value) {
			return fmt.Sprintf("%s escapes workspace: %s", key, value)
		}
	}
	return ""
}

// pathWithinAnyWorkspace reports whether path is inside the working
// directory or any of the additional allowed roots. Each root is
// canonicalized the same way as the working directory.
func pathWithinAnyWorkspace(workDir string, allowedRoots []string, path string) bool {
	if pathWithinWorkspace(workDir, path) {
		return true
	}
	for _, root := range allowedRoots {
		if pathWithinWorkspace(root, path) {
			return true
		}
	}
	return false
}

func pathWithinWorkspace(workDir string, path string) bool {
	base, err := filepath.Abs(workDir)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	// Resolve symlinks consistently on both sides. EvalSymlinks fails on
	// non-existent paths, so we resolve the longest existing prefix and
	// re-append the trailing components. This keeps base and abs in the
	// same canonical form even when the target has not been created yet.
	base = resolveSymlinks(base)
	abs = resolveSymlinks(abs)
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveSymlinks canonicalizes a path by resolving symlinks on the longest
// existing prefix. If the full path exists, it behaves like filepath.EvalSymlinks.
// If only part of the path exists, it resolves that part and appends the
// remaining components. If nothing exists, the cleaned absolute path is returned.
func resolveSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	// Walk up until we find an existing ancestor, resolve it, then re-append.
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	for dir != "/" && dir != "." {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, base)
		}
		base = filepath.Join(filepath.Base(dir), base)
		dir = filepath.Dir(dir)
	}
	return filepath.Clean(path)
}
