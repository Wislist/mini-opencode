package agent

import "testing"

func TestLocallyReviewableShellCommandForWindows(t *testing.T) {
	for _, command := range []string{"Get-Location", "pwd", "Get-ChildItem", "dir"} {
		if !locallyReviewableShellCommandForOS(command, "windows") {
			t.Errorf("%q should be locally reviewable on Windows", command)
		}
	}
	for _, command := range []string{
		"Get-ChildItem -Force",
		"dir C:\\",
		"Get-Location; Remove-Item file",
		"Write-Output $(Remove-Item file)",
	} {
		if locallyReviewableShellCommandForOS(command, "windows") {
			t.Errorf("%q must still require approval", command)
		}
	}
}

func TestLocallyReviewableShellCommandForUnixKeepsExistingAllowlist(t *testing.T) {
	for _, command := range []string{"pwd", "/bin/pwd", "/bin/ls"} {
		if !locallyReviewableShellCommandForOS(command, "darwin") {
			t.Errorf("%q should be locally reviewable on Unix", command)
		}
	}
	if locallyReviewableShellCommandForOS("ls", "darwin") {
		t.Fatal("the Unix allowlist should remain exact and conservative")
	}
}
