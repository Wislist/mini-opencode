package tui

import (
	"os"
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

// gitProcessCount / gitProcessCountHook instrument gitProcess for tests. They
// exist so a test can assert the repository check does not spawn a process per
// directory level, which is the regression that made this 41ms per call.
var (
	gitProcessCount     int
	gitProcessCountHook func()
)

// collectGitStatus gathers the branch and working-tree counts for display.
//
// It uses a single `git status --porcelain --branch` invocation. The previous
// implementation ran `git rev-parse --git-dir` once per directory while walking
// up to find the repository root, then two more processes (`rev-parse
// --abbrev-ref HEAD` and `status --porcelain`); measured at 41ms per call on an
// M2 with 368 allocations. It is called synchronously on the UI goroutine from
// key handling and session switching, so those 41ms were visible input lag.
//
// The --branch header carries the branch name, so detection and status collapse
// into one process. Repository detection is a stat walk (see isGitDir) rather
// than a subprocess walk.
func collectGitStatus(workingDir string) GitStatus {
	var st GitStatus
	if !isGitDir(workingDir) {
		return st
	}

	out, err := gitOutput(workingDir, "status", "--porcelain", "--branch")
	if err != nil {
		// A git failure inside a real repository (dubious ownership, unreadable
		// index, no commits yet) still means we know where we are; report the
		// location as available without counts rather than showing nothing.
		st.Available = true
		return st
	}

	st.Available = true
	// The first line is the branch header: "## main...origin/main [ahead 1]",
	// "## HEAD (no branch)" when detached, or "## No commits yet on main".
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	if len(lines) > 0 {
		st.Branch = parseGitBranchHeader(lines[0])
		lines = lines[1:]
	}

	for _, line := range lines {
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

// parseGitBranchHeader extracts the branch name from a `git status
// --porcelain --branch` header line, returning "" when the header does not name
// a real branch (detached HEAD, unborn branch).
func parseGitBranchHeader(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "##")
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	// "No commits yet on main" — take the branch after " on ".
	if rest, ok := strings.CutPrefix(line, "No commits yet on "); ok {
		line = strings.TrimSpace(rest)
	}
	// Drop upstream tracking: "main...origin/main [ahead 1]" -> "main".
	if idx := strings.Index(line, "..."); idx >= 0 {
		line = line[:idx]
	}
	// Drop any remaining " [ahead 1]" style annotation.
	if idx := strings.Index(line, " ["); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)
	if line == "" || line == "HEAD (no branch)" || line == "(no branch)" {
		return ""
	}
	return line
}

func gitOutput(workingDir string, args ...string) (string, error) {
	if gitProcessCountHook != nil {
		gitProcessCount++
		gitProcessCountHook()
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = workingDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// isGitDir walks up from workingDir looking for the repository root.
//
// This is a filesystem stat walk, not a subprocess walk: the previous version
// ran `git rev-parse --git-dir` at every level, so a working directory eight
// levels below the root cost eight processes before any real work started.
// A `.git` entry may be a directory (normal repository) or a file containing a
// "gitdir:" pointer (linked worktree), so both are accepted.
func isGitDir(workingDir string) bool {
	if workingDir == "" {
		return false
	}
	for {
		if isGitEntry(filepath.Join(workingDir, ".git")) {
			return true
		}
		parent := filepath.Dir(workingDir)
		if parent == workingDir {
			return false
		}
		workingDir = parent
	}
}

// isGitEntry reports whether path is a `.git` directory or worktree pointer
// file.
func isGitEntry(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return true
	}
	return info.Mode().IsRegular()
}
