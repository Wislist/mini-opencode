package tui

import (
	"testing"

	"charm.land/bubbles/v2/viewport"

	"github.com/wislist/mini-opencode/internal/config"
)

func newNameTestModel() *Model {
	return &Model{
		viewport:     viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		cfg:          &config.Config{User: "you", Assistant: "assistant"},
		streamingIdx: -1,
	}
}

// savingNameSaver mimics the app-side NameSaver: empty values keep the
// existing name, non-empty values overwrite it.
func savingNameSaver(m *Model) NameSaver {
	return func(user, assistant string) (config.Config, error) {
		cfg := *m.cfg
		if user != "" {
			cfg.User = user
		}
		if assistant != "" {
			cfg.Assistant = assistant
		}
		return cfg, nil
	}
}

func TestCommandListIncludesName(t *testing.T) {
	found := false
	for _, c := range commandList {
		if c.Name == "/name" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("commandList missing /name")
	}
}

func TestFilterCommandsMatchesName(t *testing.T) {
	got := filterCommands("/n")
	names := make([]string, 0, len(got))
	for _, c := range got {
		names = append(names, c.Name)
	}
	if !contains(names, "/name") {
		t.Fatalf("filterCommands(/n) = %v, want /name present", names)
	}
}

func TestHandleNameSetsUserOnly(t *testing.T) {
	m := newNameTestModel()
	m.SetNameSaver(savingNameSaver(m))
	m.handleName("/name user Alice")
	if m.cfg.User != "Alice" {
		t.Fatalf("User = %q, want Alice", m.cfg.User)
	}
	if m.cfg.Assistant != "assistant" {
		t.Fatalf("Assistant = %q, want unchanged \"assistant\"", m.cfg.Assistant)
	}
}

func TestHandleNameSetsAssistantOnly(t *testing.T) {
	m := newNameTestModel()
	m.SetNameSaver(savingNameSaver(m))
	m.handleName("/name assistant Bob")
	if m.cfg.Assistant != "Bob" {
		t.Fatalf("Assistant = %q, want Bob", m.cfg.Assistant)
	}
	if m.cfg.User != "you" {
		t.Fatalf("User = %q, want unchanged \"you\"", m.cfg.User)
	}
}

func TestHandleNameSetsBothWhenBare(t *testing.T) {
	m := newNameTestModel()
	m.SetNameSaver(savingNameSaver(m))
	m.handleName("/name Codex")
	if m.cfg.User != "Codex" {
		t.Fatalf("User = %q, want Codex", m.cfg.User)
	}
	if m.cfg.Assistant != "Codex" {
		t.Fatalf("Assistant = %q, want Codex", m.cfg.Assistant)
	}
}

func TestHandleNameShowKeepsNames(t *testing.T) {
	m := newNameTestModel()
	m.SetNameSaver(savingNameSaver(m))
	before := *m.cfg
	m.handleName("/name")
	if m.cfg.User != before.User || m.cfg.Assistant != before.Assistant {
		t.Fatalf("names changed: User=%q Assistant=%q", m.cfg.User, m.cfg.Assistant)
	}
}

func TestHandleNameWithoutSaverReportsError(t *testing.T) {
	m := newNameTestModel()
	// no NameSaver configured
	m.handleName("/name user Alice")
	if m.cfg.User != "you" {
		t.Fatalf("User = %q, want unchanged \"you\"", m.cfg.User)
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
