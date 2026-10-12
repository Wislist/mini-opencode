package tools

import (
	"bytes"
	"embed"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed *.md *.md.tpl
var instructionFiles embed.FS

type InstructionData struct {
	BannedCommands   string
	MaxOutputLength  int
	MaxResults       int
	RgAvailable      bool
	ShellName        string
	ShellEnvironment string
	Windows          bool
}

var instructionFileByTool = map[string]string{
	BashToolName:         "bash.md.tpl",
	ReadToolName:         "read.md",
	WriteToolName:        "write.md",
	EditToolName:         "edit.md",
	LSToolName:           "ls.md",
	GlobToolName:         "glob.md.tpl",
	GrepToolName:         "grep.md.tpl",
	JobOutputToolName:    "job_output.md",
	JobKillToolName:      "job_kill.md",
	InstallSkillToolName: "install_skill.md",
	MemoryToolName:       "memory.md",
}

func DefaultInstructionData() InstructionData {
	_, rgErr := exec.LookPath("rg")
	data := InstructionData{
		BannedCommands:  strings.Join(DefaultBannedCommands, ", "),
		MaxOutputLength: DefaultMaxOutputLength,
		MaxResults:      200,
		RgAvailable:     rgErr == nil,
	}
	if shell, err := currentShell(); err == nil {
		data.ShellName = shell.Name
		data.ShellEnvironment = shell.environmentDescription()
		data.Windows = shell.Windows
	} else {
		data.ShellName = "platform shell"
		data.ShellEnvironment = err.Error()
	}
	return data
}

func RenderToolInstructions(name string, data InstructionData) (string, error) {
	path, ok := instructionFileByTool[name]
	if !ok {
		return "", fmt.Errorf("unknown tool instructions: %s", name)
	}

	raw, err := instructionFiles.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read tool instructions %s: %w", path, err)
	}
	if !strings.HasSuffix(path, ".tpl") {
		return strings.TrimSpace(string(raw)), nil
	}

	tpl, err := template.New(filepath.Base(path)).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse tool instructions %s: %w", path, err)
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute tool instructions %s: %w", path, err)
	}
	return strings.TrimSpace(buf.String()), nil
}
