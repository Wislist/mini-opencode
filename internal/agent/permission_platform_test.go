package agent

import "testing"

func TestLocallyReviewableShellCommandForWindows(t *testing.T) {
	for _, command := range []string{"Get-Location", "pwd", "Get-ChildItem", "dir"} {
		if !locallyReviewableShellCommandForOS(command, "windows", nil) {
			t.Errorf("%q should be locally reviewable on Windows", command)
		}
	}
	for _, command := range []string{
		"Get-ChildItem -Force",
		"dir C:\\",
		"Get-Location; Remove-Item file",
		"Write-Output $(Remove-Item file)",
	} {
		if locallyReviewableShellCommandForOS(command, "windows", nil) {
			t.Errorf("%q must still require approval", command)
		}
	}
}

func TestLocallyReviewableShellCommandForUnix(t *testing.T) {
	for _, command := range []string{"pwd", "/bin/pwd", "/bin/ls", "ls", "ls -la", "git status", "go list ./..."} {
		if !locallyReviewableShellCommandForOS(command, "darwin", nil) {
			t.Errorf("%q should be locally reviewable on Unix", command)
		}
	}
	// The allowlist stays conservative: path hijacks, globs, pipes and
	// interpreters must still require approval.
	for _, command := range []string{"/tmp/ls", "./git status", "ls *.go", "ls | grep x", "sh -c pwd", "/tmp/make test"} {
		if locallyReviewableShellCommandForOS(command, "darwin", nil) {
			t.Errorf("%q must still require approval on Unix", command)
		}
	}
}
