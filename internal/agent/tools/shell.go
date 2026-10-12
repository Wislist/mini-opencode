package tools

import (
	"fmt"
	"os/exec"
	"runtime"
)

type shellSpec struct {
	Executable string
	Name       string
	Windows    bool
}

func currentShell() (shellSpec, error) {
	return resolveShell(runtime.GOOS, exec.LookPath)
}

func resolveShell(goos string, lookPath func(string) (string, error)) (shellSpec, error) {
	if goos == "windows" {
		for _, candidate := range []struct {
			executable string
			name       string
		}{
			{executable: "pwsh.exe", name: "PowerShell 7"},
			{executable: "powershell.exe", name: "Windows PowerShell"},
		} {
			path, err := lookPath(candidate.executable)
			if err == nil {
				return shellSpec{Executable: path, Name: candidate.name, Windows: true}, nil
			}
		}
		return shellSpec{}, fmt.Errorf("Windows shell unavailable: install pwsh.exe or ensure powershell.exe is on PATH")
	}

	path, err := lookPath("bash")
	if err != nil {
		return shellSpec{}, fmt.Errorf("bash is required on %s: %w", goos, err)
	}
	return shellSpec{Executable: path, Name: "bash"}, nil
}

func (s shellSpec) commandArgs(command string) []string {
	if s.Windows {
		return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command}
	}
	return []string{"-lc", command}
}

func (s shellSpec) environmentDescription() string {
	if s.Windows {
		return "Runs on Windows via " + s.Name + ". Use PowerShell syntax and Windows paths."
	}
	return "Runs on macOS via bash -lc using the system shell. The BSD coreutils and standard POSIX toolset are available. Use forward slashes for paths."
}
