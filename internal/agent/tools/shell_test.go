package tools

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestResolveShellForWindowsPrefersPowerShellSeven(t *testing.T) {
	lookup := fakeShellLookup(map[string]string{
		"pwsh.exe":       `C:\\Program Files\\PowerShell\\7\\pwsh.exe`,
		"powershell.exe": `C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe`,
	})

	shell, err := resolveShell("windows", lookup)
	if err != nil {
		t.Fatalf("resolveShell() error = %v", err)
	}
	if shell.Executable != `C:\\Program Files\\PowerShell\\7\\pwsh.exe` {
		t.Fatalf("executable = %q", shell.Executable)
	}
	if shell.Name != "PowerShell 7" || !shell.Windows {
		t.Fatalf("shell = %+v", shell)
	}
	want := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `Write-Output "a b"`}
	if got := shell.commandArgs(`Write-Output "a b"`); !reflect.DeepEqual(got, want) {
		t.Fatalf("commandArgs() = %#v, want %#v", got, want)
	}
}

func TestResolveShellForWindowsFallsBackToWindowsPowerShell(t *testing.T) {
	lookup := fakeShellLookup(map[string]string{
		"powershell.exe": `C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe`,
	})

	shell, err := resolveShell("windows", lookup)
	if err != nil {
		t.Fatalf("resolveShell() error = %v", err)
	}
	if shell.Name != "Windows PowerShell" || !strings.HasSuffix(strings.ToLower(shell.Executable), "powershell.exe") {
		t.Fatalf("shell = %+v", shell)
	}
}

func TestResolveShellForWindowsReportsMissingPowerShell(t *testing.T) {
	_, err := resolveShell("windows", fakeShellLookup(nil))
	if err == nil {
		t.Fatal("expected missing PowerShell error")
	}
	for _, want := range []string{"pwsh.exe", "powershell.exe"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestResolveShellForUnixUsesBash(t *testing.T) {
	lookup := fakeShellLookup(map[string]string{"bash": "/bin/bash"})
	shell, err := resolveShell("darwin", lookup)
	if err != nil {
		t.Fatalf("resolveShell() error = %v", err)
	}
	if shell.Executable != "/bin/bash" || shell.Name != "bash" || shell.Windows {
		t.Fatalf("shell = %+v", shell)
	}
	if got, want := shell.commandArgs("printf hello"), []string{"-lc", "printf hello"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commandArgs() = %#v, want %#v", got, want)
	}
}

func fakeShellLookup(paths map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path := paths[name]; path != "" {
			return path, nil
		}
		return "", errors.New("not found")
	}
}
