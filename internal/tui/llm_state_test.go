package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

func llmStateTestModel(t *testing.T, configured bool) *Model {
	t.Helper()
	cfg := config.Default()
	cfg.ProviderConfigured = configured
	cfg.ProviderConfigurationKnown = true
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return model.(*Model)
}

func TestUnconfiguredLLMUsesEmptyStateOutsideTranscript(t *testing.T) {
	m := llmStateTestModel(t, false)
	m.SetLLMStatus(LLMUnconfigured, "")
	m.Init()

	if len(m.blocks) != 0 {
		t.Fatalf("first-run guidance leaked into transcript: %q", m.blocks)
	}
	frame := plainText(m.renderFrame())
	for _, want := range []string{"No LLM configured", "Configure a provider", "/provider add"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("empty state missing %q:\n%s", want, frame)
		}
	}
	header := plainText(m.renderHeader())
	if !strings.Contains(header, "No model configured") || strings.Contains(header, "vtest · echo") {
		t.Fatalf("header presents fallback echo as configured: %q", header)
	}
}

func TestExplicitEchoKeepsNormalWelcome(t *testing.T) {
	m := llmStateTestModel(t, true)
	m.Init()

	joined := plainText(strings.Join(m.blocks, "\n"))
	if !strings.Contains(joined, "welcome to mini-opencode") {
		t.Fatalf("explicit echo lost the normal welcome: %q", joined)
	}
	if strings.Contains(plainText(m.renderHeader()), "No model configured") {
		t.Fatalf("explicit echo was presented as unconfigured: %q", plainText(m.renderHeader()))
	}
}

func TestUnavailableProviderShowsCompactDisconnectedState(t *testing.T) {
	m := llmStateTestModel(t, true)
	m.cfg.Provider = config.ProviderConfig{Name: "deepseek", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat"}
	m.SetLLMStatus(LLMUnavailable, "api key is required")
	m.Init()

	if len(m.blocks) != 0 {
		t.Fatalf("provider failure leaked into transcript: %q", m.blocks)
	}
	frame := plainText(m.renderFrame())
	if !strings.Contains(frame, "deepseek is unavailable") || !strings.Contains(frame, "/provider add") {
		t.Fatalf("unavailable empty state is not actionable:\n%s", frame)
	}
	if !strings.Contains(plainText(m.renderHeader()), "deepseek · disconnected") {
		t.Fatalf("header missing disconnected state: %q", plainText(m.renderHeader()))
	}
}

func TestUnavailableSendPreservesDraftAndPrefillsRecoveryForm(t *testing.T) {
	m := llmStateTestModel(t, true)
	m.cfg.Provider = config.ProviderConfig{
		Name:    "deepseek",
		BaseURL: "https://api.deepseek.com",
		Model:   "deepseek-chat",
	}
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))
	m.SetLLMStatus(LLMUnavailable, "api key is required")

	m.handleInput("resume after reconnect")

	if m.state != stateProviderForm {
		t.Fatalf("state = %v, want provider form", m.state)
	}
	if got := m.input.Value(); got != "resume after reconnect" {
		t.Fatalf("draft = %q, want preserved input", got)
	}
	if got := m.providerForm.name.Value(); got != "deepseek" {
		t.Fatalf("provider name = %q, want deepseek", got)
	}
	if got := m.providerForm.url.Value(); got != "https://api.deepseek.com" {
		t.Fatalf("provider url = %q", got)
	}
	if m.providerForm.focus != 2 {
		t.Fatalf("form focus = %d, want api key field", m.providerForm.focus)
	}
	if got := len(m.Runtime().Messages()); got != 0 {
		t.Fatalf("fallback echo received %d messages", got)
	}
}

func TestUnconfiguredSendPreservesDraftAndOpensProviderSetup(t *testing.T) {
	m := llmStateTestModel(t, false)
	m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))
	m.SetLLMStatus(LLMUnconfigured, "")
	before := len(m.blocks)

	m.handleInput("please inspect this repository")

	if m.state != stateProviderSetup {
		t.Fatalf("state = %v, want provider setup", m.state)
	}
	if got := m.input.Value(); got != "please inspect this repository" {
		t.Fatalf("draft = %q, want preserved input", got)
	}
	if len(m.blocks) != before {
		t.Fatalf("blocked send changed transcript: %q", m.blocks[before:])
	}
	if got := len(m.Runtime().Messages()); got != 0 {
		t.Fatalf("fallback echo received %d messages", got)
	}
}

func TestUnconfiguredEmptyEnterOpensProviderSetup(t *testing.T) {
	m := llmStateTestModel(t, false)
	m.SetLLMStatus(LLMUnconfigured, "")

	m = pressKey(m, key("enter"))

	if m.state != stateProviderSetup {
		t.Fatalf("state = %v, want provider setup", m.state)
	}
}
