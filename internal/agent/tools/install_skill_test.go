package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

func TestInstallSkillToolCurated(t *testing.T) {
	dir := t.TempDir()
	tool := NewInstallSkillTool(FileOptions{WorkDir: dir})
	args, _ := json.Marshal(map[string]string{"source": "commit"})
	out, err := tool.Run(context.Background(), agent.ToolInput{
		CallID:    "1",
		Name:      InstallSkillToolName,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "installed skill") {
		t.Fatalf("content = %q", out.Content)
	}
	if !strings.Contains(out.Content, "SKILL.md") {
		t.Fatalf("expected SKILL.md in content: %q", out.Content)
	}
	if out.Metadata["source_kind"] != "curated" {
		t.Fatalf("metadata = %+v", out.Metadata)
	}
	if _, err := os.Stat(out.Metadata["path"].(string)); err != nil {
		t.Fatalf("installed file missing: %v", err)
	}
}

func TestInstallSkillToolDefinition(t *testing.T) {
	tool := NewInstallSkillTool(FileOptions{WorkDir: t.TempDir()})
	def := tool.Definition()
	if def.Name != InstallSkillToolName {
		t.Fatalf("name = %q", def.Name)
	}
	if def.Behavior.ReadOnly {
		t.Fatal("install_skill must not be read-only")
	}
}
