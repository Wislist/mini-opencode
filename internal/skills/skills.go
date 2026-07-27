package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	SkillFileName  = "SKILL.md"
	MaxDescription = 280
)

// Skill is the metadata exposed to the agent prompt.
type Skill struct {
	Name        string
	Description string
	Location    string
}

// InstallResult describes a finished installation.
type InstallResult struct {
	Name       string
	Path       string
	SourceKind string
}

// SourceKindValue classifies where a skill is installed from.
type SourceKindValue string

const (
	SourceCurated SourceKindValue = "curated"
	SourceLocal   SourceKindValue = "local"
	SourceGitHub  SourceKindValue = "github"
)

// Dir returns the per-workspace skills directory: .mini-opencode/skills.
func Dir(workDir string) string {
	return filepath.Join(workDir, ".mini-opencode", "skills")
}

// PathOf returns the SKILL.md path for a named skill.
func PathOf(workDir, name string) string {
	return filepath.Join(Dir(workDir), name, SkillFileName)
}

// LoadSkills discovers installed skills under workDir. Returns nil when the
// skills directory does not exist yet.
func LoadSkills(workDir string) ([]Skill, error) {
	root := Dir(workDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var loaded []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), SkillFileName)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name, desc := parseSkill(data)
		if name == "" {
			name = entry.Name()
		}
		loaded = append(loaded, Skill{
			Name:        name,
			Description: desc,
			Location:    path,
		})
	}
	return loaded, nil
}

// Install installs a skill from a curated name, a local path, or a GitHub
// repository reference into the workspace skills directory.
func Install(workDir, source, name string) (InstallResult, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return InstallResult{}, errors.New("source is required")
	}

	// 1. Curated registry.
	if content, ok := Curated(source); ok {
		if name == "" {
			name = source
		}
		if err := validateName(name); err != nil {
			return InstallResult{}, err
		}
		path, err := writeSkill(workDir, name, content)
		return InstallResult{Name: name, Path: path, SourceKind: string(SourceCurated)}, err
	}

	// 2. Local file or directory referenced from the workspace.
	localPath := source
	if !filepath.IsAbs(localPath) {
		localPath = filepath.Join(workDir, source)
	}
	if info, err := os.Stat(localPath); err == nil {
		content, err := readLocalSkill(localPath, info)
		if err != nil {
			return InstallResult{}, err
		}
		if name == "" {
			name = deriveLocalName(localPath, info)
		}
		if err := validateName(name); err != nil {
			return InstallResult{}, err
		}
		path, err := writeSkill(workDir, name, string(content))
		return InstallResult{Name: name, Path: path, SourceKind: string(SourceLocal)}, err
	}

	// 3. GitHub reference: clone shallow, copy SKILL.md out of the repo.
	if isGitHubRef(source) {
		return installFromGitHub(workDir, source, name)
	}

	return InstallResult{}, fmt.Errorf(
		"unsupported skill source %q (use a curated name, a local path, or a github repo path)", source)
}

func readLocalSkill(path string, info os.FileInfo) ([]byte, error) {
	if info.IsDir() {
		data, err := os.ReadFile(filepath.Join(path, SkillFileName))
		if err != nil {
			return nil, fmt.Errorf("directory has no %s: %w", SkillFileName, err)
		}
		return data, nil
	}
	return os.ReadFile(path)
}

func deriveLocalName(path string, info os.FileInfo) string {
	if info.IsDir() {
		return sanitizeName(filepath.Base(path))
	}
	base := filepath.Base(path)
	if strings.EqualFold(base, SkillFileName) {
		return sanitizeName(filepath.Base(filepath.Dir(path)))
	}
	return sanitizeName(strings.TrimSuffix(base, filepath.Ext(base)))
}

func writeSkill(workDir, name, content string) (string, error) {
	path := PathOf(workDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

var skillNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateName(name string) error {
	if !skillNameRe.MatchString(name) {
		return fmt.Errorf("invalid skill name %q (use letters, digits, '.', '_', '-')", name)
	}
	return nil
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "-")
	if !skillNameRe.MatchString(s) {
		return "skill"
	}
	return s
}

// parseSkill extracts a name and a short description from a SKILL.md body.
// The first "# heading" (if any) is the name; the first non-heading,
// non-empty line is the description.
func parseSkill(data []byte) (name, description string) {
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") && name == "" {
			name = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		description = line
		break
	}
	return name, clampDescription(description)
}

func clampDescription(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= MaxDescription {
		return s
	}
	return s[:MaxDescription-3] + "..."
}
