package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestPathWithinAnyWorkspaceAllowsAllowedRoot(t *testing.T) {
	workDir := t.TempDir()
	extra := t.TempDir()
	target := filepath.Join(extra, "file.txt")

	if !pathWithinAnyWorkspace(workDir, []string{extra}, target) {
		t.Fatal("path inside an allowed root was rejected")
	}
	if pathWithinAnyWorkspace(workDir, []string{extra}, "/etc/hosts") {
		t.Fatal("path outside all roots was accepted")
	}
}

func TestDefaultPermissionPolicyAllowsPathInAllowedRoot(t *testing.T) {
	workDir := t.TempDir()
	extra := t.TempDir()
	target := filepath.Join(extra, "notes.md")

	policy := NewDefaultPermissionPolicyWithRoots(workDir, []string{extra})
	decision := policy.Check(context.Background(), ToolCall{
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"` + target + `"}`),
	}, ToolDefinition{Behavior: ToolBehavior{ReadOnly: true}})

	if decision.Action != PermissionAllow {
		t.Fatalf("decision = %#v; want allow", decision)
	}
}

func TestDefaultPermissionPolicyDeniesPathOutsideAllowedRoots(t *testing.T) {
	workDir := t.TempDir()
	extra := t.TempDir()

	policy := NewDefaultPermissionPolicyWithRoots(workDir, []string{extra})
	decision := policy.Check(context.Background(), ToolCall{
		Name:      "read",
		Arguments: json.RawMessage(`{"path":"/etc/hosts"}`),
	}, ToolDefinition{Behavior: ToolBehavior{ReadOnly: true}})

	if decision.Action != PermissionDeny {
		t.Fatalf("decision = %#v; want deny", decision)
	}
}
