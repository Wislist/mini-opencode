package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPermissionPolicyAllowsReadOnlyTool(t *testing.T) {
	policy := NewDefaultPermissionPolicy(t.TempDir())
	decision := policy.Check(context.Background(), ToolCall{
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"file.txt"}`),
	}, ToolDefinition{Behavior: ToolBehavior{ReadOnly: true}})

	if decision.Action != PermissionAllow {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestDefaultPermissionPolicyDeniesBannedCommand(t *testing.T) {
	policy := NewDefaultPermissionPolicy(t.TempDir())
	decision := policy.Check(context.Background(), ToolCall{
		Name:      "bash",
		Arguments: json.RawMessage(`{"command":"git push origin main"}`),
	}, ToolDefinition{Behavior: ToolBehavior{Dangerous: true}})

	if decision.Action != PermissionDeny {
		t.Fatalf("decision = %#v", decision)
	}
	if !strings.Contains(decision.Reason, "git push") {
		t.Fatalf("reason = %q", decision.Reason)
	}
}

func TestDefaultPermissionPolicyDeniesWorkspaceEscape(t *testing.T) {
	policy := NewDefaultPermissionPolicy(t.TempDir())
	decision := policy.Check(context.Background(), ToolCall{
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"../outside.txt"}`),
	}, ToolDefinition{Behavior: ToolBehavior{ReadOnly: true}})

	if decision.Action != PermissionDeny {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestDefaultPermissionPolicyConfirmsDangerousTool(t *testing.T) {
	policy := NewDefaultPermissionPolicy(t.TempDir())
	decision := policy.Check(context.Background(), ToolCall{
		Name:      "write",
		Arguments: json.RawMessage(`{"path":"file.txt"}`),
	}, ToolDefinition{Behavior: ToolBehavior{Dangerous: true, RequiresConfirmation: true}})

	if decision.Action != PermissionConfirm {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestPathWithinWorkspaceAllowsWorkspaceRootItself(t *testing.T) {
	dir := t.TempDir()
	// The workspace root, expressed both relatively and absolutely, must be
	// considered inside the workspace. This is the regression for the bug
	// where `ls <workspace-root>` was denied as an escape.
	if !pathWithinWorkspace(dir, ".") {
		t.Error("pathWithinWorkspace(dir, \".\") = false, want true")
	}
	if !pathWithinWorkspace(dir, dir) {
		t.Errorf("pathWithinWorkspace(dir, dir) = false, want true")
	}
}

func TestDefaultPermissionPolicyAllowsWorkspaceRootPath(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	policy := NewDefaultPermissionPolicy(dir)
	decision := policy.Check(context.Background(), ToolCall{
		Name:      "ls",
		Arguments: json.RawMessage(`{"path":"` + abs + `"}`),
	}, ToolDefinition{Behavior: ToolBehavior{ReadOnly: true}})

	if decision.Action != PermissionAllow {
		t.Fatalf("ls on workspace root denied: %#v", decision)
	}
}

func TestPathWithinWorkspaceAllowsNonExistentSubPath(t *testing.T) {
	dir := t.TempDir()
	// A path that does not exist yet (e.g. .mini-opencode/skills) must still
	// be considered inside the workspace so read-only tools like ls are not
	// wrongly denied.
	nonexistent := filepath.Join(dir, ".mini-opencode", "skills")
	if !pathWithinWorkspace(dir, nonexistent) {
		t.Errorf("pathWithinWorkspace denied non-existent sub-path: %s", nonexistent)
	}
	// Relative form should also pass.
	if !pathWithinWorkspace(dir, filepath.Join(".mini-opencode", "skills")) {
		t.Error("pathWithinWorkspace denied relative non-existent sub-path")
	}
}

func TestResolveSymlinksNonExistentPath(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Canonicalize the workspace root the same way resolveSymlinks does
	// (macOS symlinks /var -> /private/var).
	wantBase, err := filepath.EvalSymlinks(abs)
	if err != nil {
		wantBase = abs
	}
	// Resolving a non-existent path should not panic or return empty; the
	// result must start with the resolved workspace root, proving the
	// existing ancestor was canonicalized and the trailing components were
	// re-appended.
	got := resolveSymlinks(filepath.Join(abs, "sub", "dir"))
	if got == "" {
		t.Fatal("resolveSymlinks returned empty for non-existent path")
	}
	if !strings.HasPrefix(got, wantBase+string(filepath.Separator)) {
		t.Errorf("resolveSymlinks result %q does not start with resolved workspace %q", got, wantBase)
	}
}

func TestToolRegistryReturnsPermissionMetadata(t *testing.T) {
	registry := NewToolRegistry()
	registry.SetPermissionPolicy(NewDefaultPermissionPolicy(t.TempDir()))
	if err := registry.Register(permissionTestTool{}); err != nil {
		t.Fatal(err)
	}

	result := registry.Run(context.Background(), ToolCall{
		ID:        "call-1",
		Name:      "write_like",
		Arguments: json.RawMessage(`{"path":"file.txt"}`),
	})
	if result.Error == "" {
		t.Fatal("expected permission error")
	}
	if result.Metadata["permission"] != string(PermissionConfirm) {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

type permissionTestTool struct{}

func (permissionTestTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "write_like",
		Description: "test tool",
		InputSchema: map[string]any{"type": "object"},
		Behavior:    ToolBehavior{Dangerous: true, RequiresConfirmation: true},
	}
}

func (permissionTestTool) Run(ctx context.Context, input ToolInput) (ToolOutput, error) {
	return ToolOutput{Content: "ran"}, nil
}
