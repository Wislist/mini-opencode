package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPermissionModes(t *testing.T) {
	for _, tc := range []struct {
		name, tool, args string
		behavior         ToolBehavior
		ask, auto, full  PermissionAction
	}{
		{"read", "read", `{"path":"a"}`, ToolBehavior{ReadOnly: true}, PermissionAllow, PermissionAllow, PermissionAllow},
		{"write", "write", `{"path":"a","content":"x"}`, ToolBehavior{Dangerous: true, RequiresConfirmation: true}, PermissionConfirm, PermissionAllow, PermissionAllow},
		{"edit", "edit", `{"path":"a"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionAllow, PermissionAllow},
		{"outside", "read", `{"path":"../outside"}`, ToolBehavior{ReadOnly: true}, PermissionDeny, PermissionDeny, PermissionAllow},
		{"pwd", "bash", `{"command":"pwd"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionAllow, PermissionAllow},
		{"foreground pwd", "bash", `{"command":"pwd","run_in_background":false}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionAllow, PermissionAllow},
		{"background pwd", "bash", `{"command":"pwd","run_in_background":true}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"invalid background flag", "bash", `{"command":"pwd","run_in_background":"false"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"outside working directory", "bash", `{"command":"pwd","working_dir":"../outside"}`, ToolBehavior{Dangerous: true}, PermissionDeny, PermissionDeny, PermissionAllow},
		{"shell chain", "bash", `{"command":"pwd; touch /tmp/no"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"shell substitution", "bash", `{"command":"echo $(touch /tmp/no)"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"git config execution", "bash", `{"command":"git -c core.pager=evil log"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"make runs project code", "bash", `{"command":"make test"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionAllow, PermissionAllow},
		{"ls listing", "bash", `{"command":"ls -la"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionAllow, PermissionAllow},
		{"git status", "bash", `{"command":"git status --short"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionAllow, PermissionAllow},
		{"go test", "bash", `{"command":"go test ./..."}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionAllow, PermissionAllow},
		{"allowlisted program with a chain", "bash", `{"command":"make test; touch x"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"unlisted build tool", "bash", `{"command":"cargo test"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"bare interpreter", "bash", `{"command":"python script.py"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"git config execution still asks", "bash", `{"command":"git -c core.pager=evil log"}`, ToolBehavior{Dangerous: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"external tool", "mcp__write", `{}`, ToolBehavior{RequiresConfirmation: true}, PermissionConfirm, PermissionConfirm, PermissionAllow},
		{"legacy ban", "bash", `{"command":"git push origin main"}`, ToolBehavior{Dangerous: true}, PermissionDeny, PermissionDeny, PermissionAllow},
		{"bad JSON", "write", `{`, ToolBehavior{Dangerous: true}, PermissionDeny, PermissionDeny, PermissionDeny},
		{"null JSON", "write", `null`, ToolBehavior{Dangerous: true}, PermissionDeny, PermissionDeny, PermissionDeny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for mode, want := range map[PermissionMode]PermissionAction{PermissionModeAsk: tc.ask, PermissionModeAutoReview: tc.auto, PermissionModeFullAccess: tc.full} {
				p, err := NewModePermissionPolicy(t.TempDir(), nil, mode)
				if err != nil {
					t.Fatal(err)
				}
				got := p.Check(context.Background(), ToolCall{Name: tc.tool, Arguments: json.RawMessage(tc.args)}, ToolDefinition{Behavior: tc.behavior})
				if got.Action != want {
					t.Errorf("%s: got %+v want %s", mode, got, want)
				}
			}
		})
	}
}

func TestLocallyReviewableShellCommands(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		goos    string
		want    bool
	}{
		{"pwd", "pwd", "darwin", true},
		{"bin pwd", "/bin/pwd", "darwin", true},
		{"bin ls", "/bin/ls", "darwin", true},
		{"bare ls", "ls", "darwin", true},
		{"ls flags", "ls -la internal/agent", "darwin", true},
		{"ls glob rejected", "ls *.go", "darwin", false},
		{"path hijack ls", "/tmp/ls", "darwin", false},
		{"relative git", "./git status", "darwin", false},
		{"git status", "git status", "darwin", true},
		{"git log", "git log --oneline -5", "darwin", true},
		{"git diff", "git diff HEAD~1", "darwin", true},
		{"git show", "git show HEAD", "darwin", true},
		{"git blame", "git blame internal/agent/permission.go", "darwin", true},
		{"git rev-parse", "git rev-parse HEAD", "darwin", true},
		{"git ls-files", "git ls-files", "darwin", true},
		{"git describe", "git describe --tags", "darwin", true},
		{"git shortlog", "git shortlog -sn", "darwin", true},
		{"git remote verbose", "git remote -v", "darwin", true},
		{"git remote bare", "git remote", "darwin", true},
		{"git remote add mutates", "git remote add origin x", "darwin", false},
		{"git branch list", "git branch", "darwin", true},
		{"git branch -a", "git branch -a", "darwin", true},
		{"git branch delete", "git branch -D feature", "darwin", false},
		{"git branch create", "git branch newbranch", "darwin", false},
		{"go version", "go version", "darwin", true},
		{"go env", "go env GOPATH", "darwin", true},
		{"go env write rejected", "go env -w GOPATH=/tmp", "darwin", false},
		{"go list", "go list ./...", "darwin", true},
		{"go doc", "go doc fmt.Println", "darwin", true},
		{"go test", "go test ./...", "darwin", true},
		{"go build", "go build ./...", "darwin", true},
		{"go run", "go run . --flag", "darwin", true},
		{"go vet", "go vet ./...", "darwin", true},
		{"go generate runs arbitrary directives", "go generate ./...", "darwin", false},
		{"go test with an absolute output path", "go test -o /tmp/x ./...", "darwin", false},
		{"go subcommand typo", "go testx ./...", "darwin", false},
		{"make", "make test", "darwin", true},
		{"make bare", "make", "darwin", true},
		{"npm test", "npm test", "darwin", true},
		{"npm run", "npm run build", "darwin", true},
		{"npm ci", "npm ci", "darwin", true},
		{"bare npm can publish", "npm publish", "darwin", false},
		{"pytest", "pytest -q", "darwin", true},
		{"python -m pytest", "python -m pytest tests", "darwin", true},
		{"bare python", "python script.py", "darwin", false},
		{"bare node", "node script.js", "darwin", false},
		{"npx fetches packages", "npx cowsay hi", "darwin", false},
		{"cargo", "cargo test", "darwin", false},
		{"command chain", "pwd; touch /tmp/no", "darwin", false},
		{"command and", "ls && rm -rf /", "darwin", false},
		{"pipe", "ls | grep x", "darwin", false},
		{"redirect", "ls > out", "darwin", false},
		{"substitution", "echo $(touch /tmp/no)", "darwin", false},
		{"backtick", "echo `touch /tmp/no`", "darwin", false},
		{"newline", "pwd\nrm -rf /", "darwin", false},
		{"subshell braces", "git status {x}", "darwin", false},
		{"whitespace only", "   ", "darwin", false},
		{"empty", "", "darwin", false},
		{"unterminated quote", `git log --grep="fix`, "darwin", false},
		{"quoted arg", `git log --grep="fix bug"`, "darwin", true},
		{"unknown program", "cargo test", "darwin", false},
		{"shell interpreter", "sh -c pwd", "darwin", false},
		{"windows dir", "Get-ChildItem", "windows", true},
		{"windows pwd", "pwd", "windows", true},
		{"windows ls", "ls", "windows", true},
		{"windows git status", "git status", "windows", true},
		{"windows remove", "Remove-Item -Recurse -Force x", "windows", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := locallyReviewableShellCommandForOS(tc.command, tc.goos, nil); got != tc.want {
				t.Errorf("locallyReviewableShellCommandForOS(%q, %q) = %v, want %v", tc.command, tc.goos, got, tc.want)
			}
		})
	}
}

func TestPermissionModeRootsAndSymlinks(t *testing.T) {
	root, extra, outside := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	p, _ := NewModePermissionPolicy(root, []string{extra}, PermissionModeAutoReview)
	for _, tc := range []struct {
		path string
		want PermissionAction
	}{
		{filepath.Join(extra, "new"), PermissionAllow},
		{filepath.Join(root, "escape", "new"), PermissionDeny},
		{root + "-sibling/file", PermissionDeny},
	} {
		args, _ := json.Marshal(map[string]string{"path": tc.path})
		if got := p.Check(context.Background(), ToolCall{Name: "write", Arguments: args}, ToolDefinition{Behavior: ToolBehavior{Dangerous: true}}); got.Action != tc.want {
			t.Fatalf("%s: %+v", tc.path, got)
		}
	}
}

func TestPermissionCommandBoundaries(t *testing.T) {
	for _, tc := range []struct {
		input   string
		mode    PermissionMode
		changed bool
	}{
		{"/permissions", PermissionModeAsk, false},
		{"/permissions ask", PermissionModeAsk, false},
		{"/permissions typo", PermissionModeAsk, false},
		{"/permissions full-access", PermissionModeAsk, false},
		{"/permissions full-access confirm extra", PermissionModeAsk, false},
		{"/permissions ask confirm", PermissionModeAsk, false},
		{"/permissions auto-review", PermissionModeAutoReview, true},
		{"/permissions full-access confirm", PermissionModeFullAccess, true},
		{"", PermissionModeAsk, false},
	} {
		next, changed, message := PermissionCommand(PermissionModeAsk, tc.input)
		if next != tc.mode || changed != tc.changed || message == "" {
			t.Fatalf("%q: %s %v %q", tc.input, next, changed, message)
		}
	}
}

type modeTestTool struct{ runs *int }

func (t modeTestTool) Definition() ToolDefinition {
	return ToolDefinition{Name: "write", Behavior: ToolBehavior{Dangerous: true, RequiresConfirmation: true}}
}
func (t modeTestTool) Run(context.Context, ToolInput) (ToolOutput, error) {
	*t.runs++
	return ToolOutput{Content: "done"}, nil
}

func TestPermissionModesRuntimeHooksAndConfirmer(t *testing.T) {
	for _, mode := range []PermissionMode{PermissionModeAsk, PermissionModeAutoReview, PermissionModeFullAccess} {
		for _, scenario := range []string{"normal", "plan", "protected"} {
			t.Run(string(mode)+"/"+scenario, func(t *testing.T) {
				root := t.TempDir()
				path := "file"
				if scenario == "protected" {
					path = ".git/config"
				}
				args, _ := json.Marshal(map[string]string{"path": path, "content": "x"})
				provider := &scriptedProvider{responses: []AssistantResponse{{ToolCalls: []ToolCall{{ID: "1", Name: "write", Arguments: args}}}, {Content: "done"}}}
				p, _ := NewModePermissionPolicy(root, nil, mode)
				runs, confirms := 0, 0
				rt := NewRuntime(provider, WithPermissionPolicy(p), WithTool(modeTestTool{&runs}), WithHook(NewSafetyHook(root)), WithHook(NewPlanModeHook(scenario == "plan")), WithPermissionConfirmer(func(context.Context, ToolCall, ToolResult) bool { confirms++; return false }))
				if err := rt.Run(context.Background(), "test", nil); err != nil {
					t.Fatal(err)
				}
				wantRuns, wantConfirms := 0, 0
				if scenario == "normal" {
					if mode == PermissionModeAsk {
						wantConfirms = 1
					} else {
						wantRuns = 1
					}
				}
				if runs != wantRuns || confirms != wantConfirms {
					t.Fatalf("runs %d/%d confirms %d/%d", runs, wantRuns, confirms, wantConfirms)
				}
			})
		}
	}
}

func TestPermissionModeValidationAndCancellation(t *testing.T) {
	p, err := NewModePermissionPolicy(t.TempDir(), nil, "")
	if err != nil || p.Mode() != PermissionModeAsk {
		t.Fatalf("default: %v %v", p, err)
	}
	if err := p.SetMode("unknown"); err == nil || p.Mode() != PermissionModeAsk {
		t.Fatal("invalid mode changed policy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, mode := range []PermissionMode{PermissionModeAsk, PermissionModeAutoReview, PermissionModeFullAccess} {
		if err := p.SetMode(mode); err != nil {
			t.Fatal(err)
		}
		if got := p.Check(ctx, ToolCall{Arguments: json.RawMessage(`{}`)}, ToolDefinition{}); got.Action != PermissionDeny {
			t.Fatalf("cancelled: %+v", got)
		}
	}
}

func TestPermissionModeConcurrentAccess(t *testing.T) {
	p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAsk)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = p.SetMode(PermissionModeFullAccess)
				_ = p.FullAccess()
				_ = p.SetMode(PermissionModeAsk)
				p.Check(context.Background(), ToolCall{Arguments: json.RawMessage(`{}`)}, ToolDefinition{})
			}
		}()
	}
	wg.Wait()
}
