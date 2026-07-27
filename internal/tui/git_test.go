package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectGitStatusCleanRepo(t *testing.T) {
	dir := gitInitRepo(t)
	writeFile(t, dir, "a.txt", "hello")
	if out, err := exec.Command("git", "-C", dir, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	st := collectGitStatus(dir)
	if !st.Available {
		t.Fatal("expected git status available")
	}
	if st.Branch == "" {
		t.Fatal("expected non-empty branch")
	}
	if st.IsDirty() {
		t.Fatalf("expected clean, got %+v", st)
	}
}

func TestCollectGitStatusDirtyCounts(t *testing.T) {
	dir := gitInitRepo(t)
	writeFile(t, dir, "committed.txt", "1")
	if out, err := exec.Command("git", "-C", dir, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// staged new file
	writeFile(t, dir, "staged.txt", "2")
	if out, err := exec.Command("git", "-C", dir, "add", "staged.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add staged: %v\n%s", err, out)
	}
	// modified (tracked, not staged)
	writeFile(t, dir, "committed.txt", "changed")
	// untracked
	writeFile(t, dir, "untracked.txt", "3")

	st := collectGitStatus(dir)
	if st.Staged != 1 {
		t.Errorf("staged = %d, want 1", st.Staged)
	}
	if st.Modified != 1 {
		t.Errorf("modified = %d, want 1", st.Modified)
	}
	if st.Untracked != 1 {
		t.Errorf("untracked = %d, want 1", st.Untracked)
	}
	if !st.IsDirty() {
		t.Error("expected dirty")
	}
}

func TestCollectGitStatusNonRepo(t *testing.T) {
	dir := t.TempDir()
	st := collectGitStatus(dir)
	if st.Available {
		t.Fatal("expected git status unavailable for non-repo")
	}
}
