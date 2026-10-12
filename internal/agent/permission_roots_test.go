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
	outside := filepath.Join(t.TempDir(), "outside.txt")

	if !pathWithinAnyWorkspace(workDir, []string{extra}, target) {
		t.Fatal("path inside an allowed root was rejected")
	}
	if pathWithinAnyWorkspace(workDir, []string{extra}, outside) {
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
	outside := filepath.Join(t.TempDir(), "outside.txt")

	policy := NewDefaultPermissionPolicyWithRoots(workDir, []string{extra})
	args, err := json.Marshal(map[string]string{"path": outside})
	if err != nil {
		t.Fatal(err)
	}
	decision := policy.Check(context.Background(), ToolCall{
		Name:      "read",
		Arguments: args,
	}, ToolDefinition{Behavior: ToolBehavior{ReadOnly: true}})

	if decision.Action != PermissionDeny {
		t.Fatalf("decision = %#v; want deny", decision)
	}
}
