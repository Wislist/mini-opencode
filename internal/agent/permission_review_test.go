package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func reviewBashDefinition() ToolDefinition {
	return ToolDefinition{Name: "bash", Behavior: ToolBehavior{Dangerous: true, RequiresConfirmation: true}}
}

func reviewShellArguments(command string) json.RawMessage {
	data, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		panic(err)
	}
	return data
}

func reviewCheck(t *testing.T, p *ModePermissionPolicy, command string) PermissionDecision {
	t.Helper()
	return p.Check(context.Background(), ToolCall{Name: "bash", Arguments: reviewShellArguments(command)}, reviewBashDefinition())
}

type stubReviewer struct {
	allowed bool
	reason  string
	delay   time.Duration
	calls   int
	seen    []string
}

func (s *stubReviewer) ReviewCommand(ctx context.Context, command string) (bool, string) {
	s.calls++
	s.seen = append(s.seen, command)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return false, "review unavailable: " + ctx.Err().Error()
		}
	}
	return s.allowed, s.reason
}

func TestReviewerOnlyConsultedInAutoReviewMode(t *testing.T) {
	for _, mode := range []PermissionMode{PermissionModeAsk, PermissionModeFullAccess} {
		p, err := NewModePermissionPolicy(t.TempDir(), nil, mode)
		if err != nil {
			t.Fatal(err)
		}
		reviewer := &stubReviewer{allowed: true, reason: "safe"}
		p.SetCommandReviewer(reviewer)
		want := PermissionConfirm
		if mode == PermissionModeFullAccess {
			want = PermissionAllow
		}
		if got := reviewCheck(t, p, "terraform apply -auto-approve"); got.Action != want {
			t.Fatalf("%s: got %+v want %s", mode, got, want)
		}
		if reviewer.calls != 0 {
			t.Fatalf("%s: reviewer called %d times outside auto-review", mode, reviewer.calls)
		}
	}
}

func TestReviewerApprovesShellCommand(t *testing.T) {
	p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
	reviewer := &stubReviewer{allowed: true, reason: "runs the project's own tests"}
	p.SetCommandReviewer(reviewer)

	got := reviewCheck(t, p, "terraform apply -auto-approve")
	if got.Action != PermissionAllow {
		t.Fatalf("got %+v want allow", got)
	}
	if !strings.Contains(got.Reason, "model") || !strings.Contains(got.Reason, "runs the project's own tests") {
		t.Fatalf("reason %q should attribute the approval to the model review", got.Reason)
	}
	if reviewer.calls != 1 || reviewer.seen[0] != "terraform apply -auto-approve" {
		t.Fatalf("reviewer calls = %d, seen = %v", reviewer.calls, reviewer.seen)
	}
}

func TestReviewerDeclineKeepsApprovalWithTheUser(t *testing.T) {
	p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
	p.SetCommandReviewer(&stubReviewer{allowed: false, reason: "writes outside the workspace"})

	got := reviewCheck(t, p, "terraform apply -auto-approve")
	if got.Action != PermissionConfirm {
		t.Fatalf("got %+v want confirm", got)
	}
	if !strings.Contains(got.Reason, "writes outside the workspace") {
		t.Fatalf("reason %q should carry the reviewer's explanation", got.Reason)
	}
}

func TestReviewerUnavailableStillAsks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reviewer CommandReviewer
	}{
		{"no reviewer", nil},
		{"empty reason", &stubReviewer{allowed: false}},
		{"stale context", &stubReviewer{allowed: false, reason: "review unavailable: context deadline exceeded"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
			p.SetCommandReviewer(tc.reviewer)
			if got := reviewCheck(t, p, "terraform apply -auto-approve"); got.Action != PermissionConfirm {
				t.Fatalf("got %+v want confirm", got)
			}
		})
	}
}

func TestReviewerSkippedWhenLocalRulesAlreadyDecide(t *testing.T) {
	for _, command := range []string{"make test", "ls -la", "git status"} {
		p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
		reviewer := &stubReviewer{allowed: false, reason: "no"}
		p.SetCommandReviewer(reviewer)
		if got := reviewCheck(t, p, command); got.Action != PermissionAllow {
			t.Fatalf("%q: got %+v want allow", command, got)
		}
		if reviewer.calls != 0 {
			t.Fatalf("%q: reviewer called %d times for a locally reviewed command", command, reviewer.calls)
		}
	}
}

func TestReviewerSkippedForNonShellAndBackgroundTools(t *testing.T) {
	for _, tc := range []struct {
		name, tool string
		args       json.RawMessage
		behavior   ToolBehavior
		want       PermissionAction
	}{
		{"unknown tool", "mcp__write", json.RawMessage(`{}`), ToolBehavior{RequiresConfirmation: true}, PermissionConfirm},
		{"write", "write", json.RawMessage(`{"path":"a","content":"x"}`), ToolBehavior{Dangerous: true}, PermissionAllow},
		{"background shell", "bash", json.RawMessage(`{"command":"terraform apply -auto-approve","run_in_background":true}`), ToolBehavior{Dangerous: true}, PermissionConfirm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
			reviewer := &stubReviewer{allowed: true, reason: "safe"}
			p.SetCommandReviewer(reviewer)
			got := p.Check(context.Background(), ToolCall{Name: tc.tool, Arguments: tc.args}, ToolDefinition{Name: tc.tool, Behavior: tc.behavior})
			if got.Action != tc.want {
				t.Fatalf("got %+v want %s", got, tc.want)
			}
			if reviewer.calls != 0 {
				t.Fatalf("reviewer called %d times", reviewer.calls)
			}
		})
	}
}

func TestReviewerSkipsEmptyCommand(t *testing.T) {
	p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
	reviewer := &stubReviewer{allowed: true}
	p.SetCommandReviewer(reviewer)
	if got := reviewCheck(t, p, "   "); got.Action != PermissionConfirm {
		t.Fatalf("got %+v want confirm", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer called %d times for an empty command", reviewer.calls)
	}
}

func TestReviewerTimeoutFailsClosed(t *testing.T) {
	p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
	p.reviewTimeout = 20 * time.Millisecond
	p.SetCommandReviewer(&stubReviewer{allowed: true, reason: "safe", delay: 3 * time.Second})

	start := time.Now()
	got := reviewCheck(t, p, "terraform apply -auto-approve")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("review did not respect its timeout: %s", elapsed)
	}
	if got.Action != PermissionConfirm {
		t.Fatalf("got %+v want confirm", got)
	}
}

func TestReviewerCannotOverturnADeny(t *testing.T) {
	p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
	reviewer := &stubReviewer{allowed: true, reason: "safe"}
	p.SetCommandReviewer(reviewer)

	got := reviewCheck(t, p, "git push origin main")
	if got.Action != PermissionDeny {
		t.Fatalf("got %+v want deny", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer called %d times for a banned command", reviewer.calls)
	}
}

func TestCommandAllowlistExtraRules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rules   [][]string
		command string
		want    PermissionAction
	}{
		{"configured prefix", [][]string{{"cargo", "test"}}, "cargo test --all", PermissionAllow},
		{"configured prefix mismatch", [][]string{{"cargo", "test"}}, "cargo build", PermissionConfirm},
		{"configured program only", [][]string{{"terraform"}}, "terraform plan", PermissionAllow},
		{"configured rule is case insensitive on the program", [][]string{{"Cargo", "test"}}, "cargo test", PermissionAllow},
		{"empty token voids the rule", [][]string{{"cargo", ""}}, "cargo test", PermissionConfirm},
		{"blank token voids the rule", [][]string{{"  "}}, "cargo test", PermissionConfirm},
		{"empty rule matches nothing", [][]string{{}}, "cargo test", PermissionConfirm},
		{"nil rule matches nothing", [][]string{nil}, "cargo test", PermissionConfirm},
		{"chain cannot borrow a rule", [][]string{{"cargo", "test"}}, "cargo test; touch x", PermissionConfirm},
		{"pipe cannot borrow a rule", [][]string{{"cargo", "test"}}, "cargo test | tee x", PermissionConfirm},
		{"path program cannot borrow a rule", [][]string{{"cargo", "test"}}, "./cargo test", PermissionConfirm},
		{"absolute argument rejected", [][]string{{"cargo", "test"}}, "cargo test /etc/passwd", PermissionConfirm},
		{"parent traversal rejected", [][]string{{"cargo", "test"}}, "cargo test ../../elsewhere", PermissionConfirm},
		{"embedded traversal rejected", [][]string{{"cargo", "test"}}, "cargo test dir/../../x", PermissionConfirm},
		{"windows drive rejected", [][]string{{"cargo", "test"}}, `cargo test C:\x`, PermissionConfirm},
		{"relative argument allowed", [][]string{{"cargo", "test"}}, "cargo test --features full ./...", PermissionAllow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
			p.SetCommandAllowlist(tc.rules)
			if got := reviewCheck(t, p, tc.command); got.Action != tc.want {
				t.Fatalf("got %+v want %s", got, tc.want)
			}
		})
	}
}

func TestBuiltinCommandPrefixArgumentsAreContained(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    PermissionAction
	}{
		{"go test -exec /tmp/evil ./...", PermissionConfirm},
		{"go test -exec=/tmp/evil ./...", PermissionConfirm},
		{"go test -toolexec /tmp/evil ./...", PermissionConfirm},
		{"go build -o /tmp/evil .", PermissionConfirm},
		{"go build -o bin/agent .", PermissionAllow},
		{"make -C /tmp", PermissionConfirm},
		{"make -C sub dir", PermissionAllow},
		{"npm run build -- --outDir ../outside", PermissionConfirm},
	} {
		t.Run(tc.command, func(t *testing.T) {
			p, _ := NewModePermissionPolicy(t.TempDir(), nil, PermissionModeAutoReview)
			if got := reviewCheck(t, p, tc.command); got.Action != tc.want {
				t.Fatalf("got %+v want %s", got, tc.want)
			}
		})
	}
}

func TestNormalizeCommandAllowlist(t *testing.T) {
	got := NormalizeCommandAllowlist([][]string{
		{"cargo", "test"},
		{"  make  "},
		{"cargo", ""},
		nil,
		{},
		{"  "},
	})
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if got[0][0] != "cargo" || got[0][1] != "test" || got[1][0] != "make" {
		t.Fatalf("got %v", got)
	}
	if NormalizeCommandAllowlist(nil) != nil {
		t.Fatal("nil input should stay nil")
	}
}

func TestParseCommandReview(t *testing.T) {
	for _, tc := range []struct {
		name        string
		content     string
		wantAllowed bool
		wantReason  string
		wantOK      bool
	}{
		{"plain json", `{"allow":true,"reason":"safe"}`, true, "safe", true},
		{"fenced json", "```json\n{\"allow\":true,\"reason\":\"safe\"}\n```", true, "safe", true},
		{"trailing prose", `{"allow":true,"reason":"safe"} — this is a test runner`, true, "safe", true},
		{"denied", `{"allow":false,"reason":"deletes files"}`, false, "deletes files", true},
		{"reason optional", `{"allow":true}`, true, "", true},
		{"missing allow", `{"reason":"safe"}`, false, "", false},
		{"string allow is not a bool", `{"allow":"true"}`, false, "", false},
		{"prose only", "I think this is fine.", false, "", false},
		{"empty", "", false, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allowed, reason, ok := ParseCommandReview(tc.content)
			if allowed != tc.wantAllowed || reason != tc.wantReason || ok != tc.wantOK {
				t.Fatalf("got (%v, %q, %v) want (%v, %q, %v)", allowed, reason, ok, tc.wantAllowed, tc.wantReason, tc.wantOK)
			}
		})
	}
}
