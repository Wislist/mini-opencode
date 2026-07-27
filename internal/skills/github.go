package skills

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	githubSCP   = regexp.MustCompile(`^git@github\.com:([^/]+)/(.+)$`)
	githubHTTP  = regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/(.+)$`)
	ownerRepoRe = regexp.MustCompile(`^([A-Za-z0-9_.\-]+)/([A-Za-z0-9_.\-]+)(/.*)?$`)
)

// isGitHubRef reports whether source parses as a GitHub reference.
func isGitHubRef(source string) bool {
	url, _, _ := parseGitHubRef(source)
	return url != ""
}

// installFromGitHub shallow-clones the referenced repo and copies SKILL.md
// (optionally located at a subpath) into the workspace skills directory.
func installFromGitHub(workDir, source, name string) (InstallResult, error) {
	repoURL, subpath, repoName := parseGitHubRef(source)
	if repoURL == "" {
		return InstallResult{}, fmt.Errorf("invalid github reference: %s", source)
	}
	if name == "" {
		name = repoName
	}
	if err := validateName(name); err != nil {
		return InstallResult{}, err
	}

	tmp, err := os.MkdirTemp("", "mini-opencode-skill-")
	if err != nil {
		return InstallResult{}, err
	}
	defer os.RemoveAll(tmp)

	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return InstallResult{}, fmt.Errorf("git clone failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	src := filepath.Join(tmp, SkillFileName)
	if subpath != "" {
		src = filepath.Join(tmp, subpath, SkillFileName)
	}
	content, err := os.ReadFile(src)
	if err != nil {
		return InstallResult{}, fmt.Errorf("SKILL.md not found in repo at %q: %w", subpath, err)
	}

	path, err := writeSkill(workDir, name, string(content))
	return InstallResult{Name: name, Path: path, SourceKind: string(SourceGitHub)}, err
}

// parseGitHubRef resolves owner/repo[/subpath] and GitHub URLs to a cloneable
// HTTPS URL plus an optional subpath inside the repo.
func parseGitHubRef(source string) (repoURL, subpath, repoName string) {
	source = strings.TrimSpace(source)
	source = strings.TrimSuffix(source, ".git")

	if m := githubSCP.FindStringSubmatch(source); m != nil {
		repo, sub := splitRepoPath(m[2])
		return fmt.Sprintf("https://github.com/%s/%s.git", m[1], repo), sub, repo
	}
	if m := githubHTTP.FindStringSubmatch(source); m != nil {
		repo, sub := splitRepoPath(m[2])
		return fmt.Sprintf("https://github.com/%s/%s.git", m[1], repo), sub, repo
	}
	if m := ownerRepoRe.FindStringSubmatch(source); m != nil {
		repo := m[2]
		sub := strings.Trim(strings.TrimPrefix(m[3], "/"), "/")
		return fmt.Sprintf("https://github.com/%s/%s.git", m[1], repo), sub, repo
	}
	return "", "", ""
}

func splitRepoPath(rest string) (repo, subpath string) {
	parts := strings.SplitN(rest, "/", 2)
	repo = parts[0]
	if len(parts) == 2 {
		subpath = strings.Trim(parts[1], "/")
	}
	return repo, subpath
}
