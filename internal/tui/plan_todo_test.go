package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

func newTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func TestTodoPanelRendersAndStaysHiddenWhenEmpty(t *testing.T) {
	m := newTestModel(t)
	if panel := m.renderTodoPanel(); panel != "" {
		t.Fatalf("empty todo list rendered a panel: %q", panel)
	}

	m.Update(runtimeEventMsg{event: agent.Event{
		Type:  agent.EventTodosChanged,
		Todos: "[x] wire mcp\n[>] add todo tool\n(1/2 done)",
	}})
	if m.todos == "" {
		t.Fatal("todo event did not reach the model")
	}
	panel := m.renderTodoPanel()
	if !strings.Contains(panel, "todos:") {
		t.Fatalf("panel missing header: %q", panel)
	}
	if !strings.Contains(panel, "wire mcp") {
		t.Fatalf("panel missing items: %q", panel)
	}
	// The panel participates in the footer layout, so View() must still fit.
	view := m.View()
	if strings.TrimSpace(view) == "" {
		t.Fatal("View() produced nothing with a todo panel")
	}
}

func TestTodoTextFromJSONIgnoresEmptyLists(t *testing.T) {
	if got := todoTextFromJSON("[]"); got != "" {
		t.Fatalf("empty list rendered %q", got)
	}
	if got := todoTextFromJSON("not json"); got != "" {
		t.Fatalf("invalid json rendered %q", got)
	}
	got := todoTextFromJSON(`[{"content":"ship","status":"completed"}]`)
	if !strings.Contains(got, "[x] ship") {
		t.Fatalf("rendered = %q", got)
	}
}

func TestPermissionPromptShowsEditDiff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	m := New(&cfg, dir, "test")
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})

	args, _ := json.Marshal(map[string]any{
		"path": "a.go", "old_string": "two", "new_string": "TWO",
	})
	call := agent.ToolCall{ID: "c1", Name: "edit", Arguments: args}
	m.pendingPerm = &permissionRequestMsg{call: call, result: &agent.ToolResult{}, resp: make(chan permissionDecision, 1)}
	m.state = statePermission

	prompt := m.renderPermissionPrompt()
	if !strings.Contains(prompt, "- two") || !strings.Contains(prompt, "+ TWO") {
		t.Fatalf("permission prompt missing the diff: %q", prompt)
	}
	if !strings.Contains(prompt, "always this session") {
		t.Fatalf("permission prompt missing the always option: %q", prompt)
	}
}

func TestPermissionAlwaysAddsToolToSessionAllowlist(t *testing.T) {
	m := newTestModel(t)
	resp := make(chan permissionDecision, 1)
	m.pendingPerm = &permissionRequestMsg{
		call:   agent.ToolCall{Name: "bash"},
		result: &agent.ToolResult{},
		resp:   resp,
	}
	m.state = statePermission

	m.handlePermissionKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	decision := <-resp
	if !decision.allow || !decision.always {
		t.Fatalf("decision = %+v, want allow+always", decision)
	}
	if !m.isSessionAllowed("bash") {
		t.Fatal("tool was not allowlisted")
	}
	// The confirmer short-circuits allowlisted tools without prompting.
	if !m.MakeConfirmer()(t.Context(), agent.ToolCall{Name: "bash"}, agent.ToolResult{}) {
		t.Fatal("allowlisted tool was not approved")
	}
	if got := m.SessionAllowedTools(); len(got) != 1 || got[0] != "bash" {
		t.Fatalf("allowlist = %#v", got)
	}
}

func TestDenyAlwaysRemovesNothingFromAllowlist(t *testing.T) {
	m := newTestModel(t)
	resp := make(chan permissionDecision, 1)
	m.pendingPerm = &permissionRequestMsg{call: agent.ToolCall{Name: "write"}, resp: resp}
	m.state = statePermission

	m.resolvePermission(permissionDecision{allow: false})
	if decision := <-resp; decision.allow {
		t.Fatal("deny produced an allow decision")
	}
	if len(m.SessionAllowedTools()) != 0 {
		t.Fatalf("deny changed the allowlist: %#v", m.SessionAllowedTools())
	}
}

func TestPlanPromptRendersAndCapsLongPlans(t *testing.T) {
	m := newTestModel(t)
	long := strings.Repeat("step line\n", 40)
	m.pendingPlan = &planApprovalMsg{plan: long, resp: make(chan planDecision, 1)}
	m.state = statePlanApproval

	prompt := m.renderPlanPrompt()
	if !strings.Contains(prompt, "plan ready for approval") {
		t.Fatalf("prompt = %q", prompt)
	}
	if !strings.Contains(prompt, "more lines") {
		t.Fatalf("long plan was not capped: %q", prompt)
	}
	if len(strings.Split(prompt, "\n")) > 20 {
		t.Fatalf("prompt is too tall: %d lines", len(strings.Split(prompt, "\n")))
	}
}

func TestPlanApprovalTurnsOffPlanMode(t *testing.T) {
	m := newTestModel(t)
	hook := &agent.PlanModeHook{Active: true}
	m.SetPlanHook(hook)
	m.mode = ModePlan

	resp := make(chan planDecision, 1)
	m.pendingPlan = &planApprovalMsg{plan: "do the thing", resp: resp}
	m.state = statePlanApproval

	m.handlePlanApprovalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if decision := <-resp; !decision.approved {
		t.Fatalf("decision = %+v", decision)
	}
	if hook.Active {
		t.Fatal("plan mode stayed active after approval")
	}
	if m.mode != ModeCode {
		t.Fatalf("mode = %v, want code", m.mode)
	}

	// Rejection keeps plan mode on.
	hook.Active = true
	m.mode = ModePlan
	m.state = statePlanApproval
	m.pendingPlan = &planApprovalMsg{plan: "another", resp: make(chan planDecision, 1)}
	m.handlePlanApprovalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if !hook.Active || m.mode != ModePlan {
		t.Fatal("rejection must leave plan mode enabled")
	}
}
