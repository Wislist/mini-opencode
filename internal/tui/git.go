package tui

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// GitStatus holds a compact summary of the working tree state for display.
type GitStatus struct {
	Branch    string
	Staged    int
	Modified  int
	Untracked int
	Available bool
}

// IsDirty reports whether there are any uncommitted changes.
func (g GitStatus) IsDirty() bool {
	return g.Staged > 0 || g.Modified > 0 || g.Untracked > 0
}

// collectGitStatus runs git in workingDir to gather branch and file counts.
// It returns an empty GitStatus with Available=false if workingDir is not a
// git repository or git is unavailable.
func collectGitStatus(workingDir string) GitStatus {
	var st GitStatus
	if !isGitDir(workingDir) {
		return st
	}
	branch, err := gitOutput(workingDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return st
	}
	st.Branch = strings.TrimSpace(branch)
	if st.Branch == "" {
		return st
	}
	porcelain, err := gitOutput(workingDir, "status", "--porcelain")
	if err != nil {
		st.Available = true
		return st
	}
	st.Available = true
	for _, line := range strings.Split(strings.TrimRight(porcelain, "\n"), "\n") {
		if len(line) < 2 {
			continue
		}
		x, y := line[0], line[1]
		switch {
		case x == '?' && y == '?':
			st.Untracked++
		case x != ' ' && x != '?':
			st.Staged++
		case y != ' ' && y != '?':
			st.Modified++
		}
	}
	return st
}

func gitOutput(workingDir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workingDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// isGitDir walks up from workingDir looking for a .git directory, mirroring
// the prompt package's git-repo detection.
func isGitDir(workingDir string) bool {
	for {
		if _, err := exec.Command("git", "-C", workingDir, "rev-parse", "--git-dir").Output(); err == nil {
			return true
		}
		parent := filepath.Dir(workingDir)
		if parent == workingDir {
			return false
		}
		workingDir = parent
	}
}
