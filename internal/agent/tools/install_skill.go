package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/skills"
)

type InstallSkillTool struct {
	options      FileOptions
	instructions string
}

type installSkillArgs struct {
	Source string `json:"source"`
	Name   string `json:"name"`
}

func NewInstallSkillTool(options FileOptions) *InstallSkillTool {
	options = normalizeFileOptions(options)
	instructions, _ := RenderToolInstructions(InstallSkillToolName, options.InstructionData)
	return &InstallSkillTool{options: options, instructions: instructions}
}

func (t *InstallSkillTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        InstallSkillToolName,
		Description: "Install a skill (SKILL.md) from the curated list, a local path, or a GitHub repo path so it is available to the agent.",
		Prompt:      t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{
					"type":        "string",
					"description": "Curated skill name, a local file/dir path, or a GitHub reference (owner/repo[/subpath], or a github.com URL).",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Optional installed skill name. Defaults to the curated name, local basename, or repo name.",
				},
			},
			"required": []string{"source"},
		},
		Behavior: agent.ToolBehavior{Dangerous: true, RequiresConfirmation: true},
	}
}

func (t *InstallSkillTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args installSkillArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("install_skill: invalid args: %w", err)
	}
	res, err := skills.Install(t.options.WorkDir, args.Source, args.Name)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("install_skill: %w", err)
	}
	content, _ := os.ReadFile(res.Path)

	var b strings.Builder
	fmt.Fprintf(&b, "installed skill %q from %s\n", res.Name, res.SourceKind)
	fmt.Fprintf(&b, "location: %s\n", res.Path)
	fmt.Fprintf(&b, "curated available: %s\n", strings.Join(skills.CuratedNames(), ", "))
	b.WriteString("\n--- SKILL.md ---\n")
	b.Write(content)
	return agent.ToolOutput{
		Content: b.String(),
		Metadata: map[string]any{
			"name":        res.Name,
			"path":        res.Path,
			"source_kind": res.SourceKind,
		},
	}, nil
}
