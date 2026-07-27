package prompt

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/wislist/mini-opencode/internal/agent/templates"
)

var promptTemplates = templates.FS

type PromptKind string

const (
	PromptCoder           PromptKind = "coder"
	PromptTask            PromptKind = "task"
	PromptInitialize      PromptKind = "initialize"
	PromptAgenticFetch    PromptKind = "agentic_fetch_prompt"
	PromptSummary         PromptKind = "summary"
	PromptTitle           PromptKind = "title"
	defaultContextMaxSize            = 64 * 1024
)

var promptTemplateFiles = map[PromptKind]string{
	PromptCoder:        "coder.md.tpl",
	PromptTask:         "task.md.tpl",
	PromptInitialize:   "initialize.md.tpl",
	PromptAgenticFetch: "agentic_fetch_prompt.md.tpl",
	PromptSummary:      "summary.md",
	PromptTitle:        "title.md",
}

var DefaultContextFileCandidates = []string{
	"AGENTS.md",
	"agents.md",
	"CLAUDE.md",
	"claude.md",
	".cursorrules",
	".cursor/rules/*.md",
	".github/copilot-instructions.md",
}

type PromptContext struct {
	WorkingDir   string
	IsGitRepo    bool
	Platform     string
	Date         string
	Config       PromptConfig
	ContextFiles []ContextFile
	Skills       []Skill
}

type PromptConfig struct {
	Options PromptOptions
}

type PromptOptions struct {
	InitializeAs string
}

type ContextFile struct {
	Path    string
	Content string
}

type Skill struct {
	Name        string
	Description string
	Location    string
}

func DefaultPromptContext(workingDir string) PromptContext {
	if workingDir == "" {
		workingDir, _ = os.Getwd()
	}
	return PromptContext{
		WorkingDir: workingDir,
		IsGitRepo:  isGitRepo(workingDir),
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		Date:       time.Now().Format("2006-01-02"),
		Config: PromptConfig{
			Options: PromptOptions{
				InitializeAs: "AGENTS.md",
			},
		},
	}
}

func BuildSystemPrompt(kind PromptKind, ctx PromptContext) (string, error) {
	base, err := RenderPromptTemplate(kind, ctx)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString(strings.TrimSpace(base))
	out.WriteString("\n\n")
	writeRuntimeContext(&out, ctx)
	writeContextFiles(&out, ctx.ContextFiles)
	writeSkills(&out, ctx.Skills)
	return strings.TrimSpace(out.String()), nil
}

func RenderPromptTemplate(kind PromptKind, ctx PromptContext) (string, error) {
	path, ok := promptTemplateFiles[kind]
	if !ok {
		return "", fmt.Errorf("unknown prompt kind: %s", kind)
	}

	raw, err := promptTemplates.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt template %s: %w", path, err)
	}
	if !strings.Contains(path, ".tpl") {
		return string(raw), nil
	}

	tpl, err := template.New(filepath.Base(path)).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse prompt template %s: %w", path, err)
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("execute prompt template %s: %w", path, err)
	}
	return buf.String(), nil
}

// SummarySystemPrompt renders the conversation-summary prompt template for the
// given working directory. It is intended to be passed to Runtime.Compact.
func SummarySystemPrompt(workingDir string) (string, error) {
	return RenderPromptTemplate(PromptSummary, DefaultPromptContext(workingDir))
}

func DiscoverContextFiles(workingDir string, candidates []string) ([]ContextFile, error) {
	if len(candidates) == 0 {
		candidates = DefaultContextFileCandidates
	}

	var files []ContextFile
	for _, candidate := range candidates {
		paths := []string{candidate}
		if hasGlob(candidate) {
			pattern := candidate
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(workingDir, candidate)
			}
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return nil, err
			}
			paths = matches
		}

		for _, path := range paths {
			if !filepath.IsAbs(path) {
				path = filepath.Join(workingDir, path)
			}

			info, err := os.Stat(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			if info.IsDir() {
				continue
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			content := string(data)
			if len(content) > defaultContextMaxSize {
				content = content[:defaultContextMaxSize] + "\n\n[truncated]\n"
			}
			files = append(files, ContextFile{
				Path:    path,
				Content: content,
			})
		}
	}
	return files, nil
}

func writeRuntimeContext(out *strings.Builder, ctx PromptContext) {
	out.WriteString("<runtime_context>\n")
	fmt.Fprintf(out, "Working directory: %s\n", ctx.WorkingDir)
	if ctx.IsGitRepo {
		out.WriteString("Is git repo: yes\n")
	} else {
		out.WriteString("Is git repo: no\n")
	}
	fmt.Fprintf(out, "Platform: %s\n", ctx.Platform)
	fmt.Fprintf(out, "Today's date: %s\n", ctx.Date)
	out.WriteString("</runtime_context>\n\n")
}

func writeContextFiles(out *strings.Builder, files []ContextFile) {
	if len(files) == 0 {
		return
	}

	out.WriteString("<context_files>\n")
	for _, file := range files {
		fmt.Fprintf(out, "<context_file path=%q>\n", file.Path)
		out.WriteString(strings.TrimSpace(file.Content))
		out.WriteString("\n</context_file>\n")
	}
	out.WriteString("</context_files>\n\n")
}

func writeSkills(out *strings.Builder, skills []Skill) {
	if len(skills) == 0 {
		return
	}

	out.WriteString("<available_skills>\n")
	for _, skill := range skills {
		out.WriteString("<skill>\n")
		fmt.Fprintf(out, "  <name>%s</name>\n", skill.Name)
		fmt.Fprintf(out, "  <description>%s</description>\n", skill.Description)
		fmt.Fprintf(out, "  <location>%s</location>\n", skill.Location)
		out.WriteString("</skill>\n")
	}
	out.WriteString("</available_skills>\n\n")
	out.WriteString(`<skills_usage>
The <description> of each skill is a trigger. It is not a full specification.
If a skill matches the current task, load and follow the skill's instruction file before doing task work.
Do not infer a skill's behavior from its name or description alone.
</skills_usage>

`)
}

func isGitRepo(workingDir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(workingDir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(workingDir)
		if parent == workingDir {
			return false
		}
		workingDir = parent
	}
}

func hasGlob(path string) bool {
	return strings.ContainsAny(path, "*?[")
}
