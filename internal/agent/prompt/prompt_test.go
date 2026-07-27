package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSystemPromptIncludesRuntimeContextFilesAndSkills(t *testing.T) {
	ctx := PromptContext{
		WorkingDir: "/repo",
		IsGitRepo:  true,
		Platform:   "test/os",
		Date:       "2026-07-03",
		ContextFiles: []ContextFile{
			{Path: "/repo/AGENTS.md", Content: "Prefer small focused changes."},
		},
		Skills: []Skill{
			{Name: "go-agent", Description: "Build Go agents.", Location: "/skills/go-agent/SKILL.md"},
		},
	}

	prompt, err := BuildSystemPrompt(PromptCoder, ctx)
	if err != nil {
		t.Fatalf("BuildSystemPrompt() error = %v", err)
	}

	for _, want := range []string{
		"<critical_rules>",
		"<runtime_context>",
		"Working directory: /repo",
		"<context_files>",
		"Prefer small focused changes.",
		"<available_skills>",
		"/skills/go-agent/SKILL.md",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestDiscoverContextFilesSupportsGlob(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, ".cursor", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "go.md"), []byte("Use gofmt."), 0644); err != nil {
		t.Fatal(err)
	}

	files, err := DiscoverContextFiles(dir, []string{".cursor/rules/*.md"})
	if err != nil {
		t.Fatalf("DiscoverContextFiles() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("file count = %d, want 1", len(files))
	}
	if !strings.Contains(files[0].Content, "Use gofmt.") {
		t.Fatalf("content = %q", files[0].Content)
	}
}

func TestCoderTemplateDoesNotContainDynamicBlocks(t *testing.T) {
	data, err := promptTemplates.ReadFile("coder.md.tpl")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "{{") {
		t.Fatalf("coder template should stay static; dynamic prompt content belongs in prompt.go")
	}
}
