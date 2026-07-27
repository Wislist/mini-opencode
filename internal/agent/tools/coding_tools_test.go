package tools

import "testing"

func TestCodingToolsIncludesCommonCodingTools(t *testing.T) {
	tools := CodingTools(CodingToolOptions{WorkDir: t.TempDir()})
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Definition().Name] = true
	}
	for _, want := range []string{
		ReadToolName,
		WriteToolName,
		EditToolName,
		LSToolName,
		GlobToolName,
		GrepToolName,
		BashToolName,
		JobOutputToolName,
		JobKillToolName,
		InstallSkillToolName,
	} {
		if !got[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
}
