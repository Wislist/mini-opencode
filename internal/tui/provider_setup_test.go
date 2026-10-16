package tui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/config"
)

func unconfiguredProviderModel(t *testing.T) *Model {
	t.Helper()
	setTestHome(t, t.TempDir())
	cfg := config.Default()
	cfg.ProviderConfigured = false
	cfg.ProviderConfigurationKnown = true
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	m.SetLLMStatus(LLMUnconfigured, "")
	return m
}

func openFirstRunProviderSetup(t *testing.T, m *Model) *Model {
	t.Helper()
	m = pressKey(m, key("enter"))
	if m.state != stateProviderSetup {
		t.Fatalf("state = %v, want stateProviderSetup", m.state)
	}
	return m
}

func TestProviderSetupListsKnownAndCustomProviders(t *testing.T) {
	m := openFirstRunProviderSetup(t, unconfiguredProviderModel(t))

	rendered := plainText(m.renderProviderSetup())
	for _, want := range []string{"DeepSeek", "OpenAI", "OpenRouter", "Custom OpenAI-compatible", "Local Echo"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("provider setup missing %q:\n%s", want, rendered)
		}
	}
}

func TestProviderSetupKnownProviderOnlyAsksForAPIKey(t *testing.T) {
	m := openFirstRunProviderSetup(t, unconfiguredProviderModel(t))
	m = pressKey(m, key("enter")) // DeepSeek

	if m.state != stateProviderForm {
		t.Fatalf("state = %v, want provider form", m.state)
	}
	if !m.providerForm.known {
		t.Fatal("DeepSeek did not open the known-provider form")
	}
	if got := m.providerForm.name.Value(); got != "deepseek" {
		t.Fatalf("provider name = %q", got)
	}
	if got := m.providerForm.url.Value(); got != "https://api.deepseek.com" {
		t.Fatalf("provider url = %q", got)
	}
	if m.providerForm.focus != 2 {
		t.Fatalf("focus = %d, want API Key field", m.providerForm.focus)
	}
	rendered := plainText(m.renderProviderForm())
	for _, want := range []string{"Connect to DeepSeek", "API URL", "https://api.deepseek.com", "API Key", "ctrl+e advanced edit"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("known-provider form missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Name  ") {
		t.Fatalf("known-provider form exposed the custom name field:\n%s", rendered)
	}
}

func TestProviderSetupCustomProviderKeepsThreeFields(t *testing.T) {
	m := openFirstRunProviderSetup(t, unconfiguredProviderModel(t))
	for range 3 {
		m = pressKey(m, key("down"))
	}
	m = pressKey(m, key("enter"))

	if m.state != stateProviderForm || m.providerForm.known {
		t.Fatalf("custom selection state=%v known=%v", m.state, m.providerForm.known)
	}
	if m.providerForm.focus != 0 {
		t.Fatalf("custom form focus = %d, want name", m.providerForm.focus)
	}
	rendered := plainText(m.renderProviderForm())
	for _, want := range []string{"Name", "API URL", "API Key"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("custom form missing %q:\n%s", want, rendered)
		}
	}
}

func TestKnownProviderRequiresKeyOrEnvironment(t *testing.T) {
	m := openFirstRunProviderSetup(t, unconfiguredProviderModel(t))
	m = pressKey(m, key("enter"))

	model, cmd := m.handleKey(key("enter"))
	m = model.(*Model)

	if cmd != nil {
		t.Fatal("missing key scheduled a model request")
	}
	if m.state != stateProviderForm || !strings.Contains(m.providerForm.err, "API key") {
		t.Fatalf("missing-key validation state=%v err=%q", m.state, m.providerForm.err)
	}
}

func TestKnownProviderUsesEnvironmentKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "env-secret")
	m := openFirstRunProviderSetup(t, unconfiguredProviderModel(t))
	stub := &fetchStub{models: catalog("deepseek-chat")}
	stub.install(m)
	withTrackedRebuild(t, m)
	m = pressKey(m, key("enter"))

	model, cmd := m.handleKey(key("enter"))
	m = model.(*Model)
	if cmd == nil || m.state != stateModelsFetching {
		t.Fatalf("environment key did not start fetch: state=%v cmd=%v", m.state, cmd != nil)
	}
	m = runCmd(t, m, cmd)
	if len(stub.calls) != 1 || stub.calls[0].apiKey != "env-secret" {
		t.Fatalf("fetch calls = %+v", stub.calls)
	}
	m = pressKey(m, key("enter"))
	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle after confirmed setup", m.state)
	}
	if m.LLMStatus() != LLMReady {
		t.Fatalf("LLM status = %v, want ready", m.LLMStatus())
	}

	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}
	provider, ok := saved.ProviderByName("deepseek")
	if !ok {
		t.Fatal("saved config is missing deepseek")
	}
	if provider.APIKey != "" || provider.APIKeyEnv != "DEEPSEEK_API_KEY" {
		t.Fatalf("saved credentials = api_key %q api_key_env %q", provider.APIKey, provider.APIKeyEnv)
	}
}

func TestProviderSetupEmptyCatalogFinishesAfterSaving(t *testing.T) {
	m := openFirstRunProviderSetup(t, unconfiguredProviderModel(t))
	stub := &fetchStub{}
	stub.install(m)
	withTrackedRebuild(t, m)
	m = pressKey(m, key("enter"))
	m = typeText(m, "sk-test")

	model, cmd := m.handleKey(key("enter"))
	m = model.(*Model)
	m = runCmd(t, m, cmd)

	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle after saving an empty catalog", m.state)
	}
	if m.cfg.Provider.Name != "deepseek" {
		t.Fatalf("active provider = %q, want deepseek", m.cfg.Provider.Name)
	}
}

func TestProviderSetupKeylessCustomProviderFinishesAfterSaving(t *testing.T) {
	m := openFirstRunProviderSetup(t, unconfiguredProviderModel(t))
	withTrackedRebuild(t, m)
	for range 3 {
		m = pressKey(m, key("down"))
	}
	m = pressKey(m, key("enter"))
	m = typeIntoForm(t, m, "local", "http://localhost:11434/v1", "")
	m = pressKey(m, key("enter"))

	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle after saving a keyless custom provider", m.state)
	}
	if m.cfg.Provider.Name != "local" {
		t.Fatalf("active provider = %q, want local", m.cfg.Provider.Name)
	}
}

func TestProviderSetupCancelDoesNotWriteGlobalConfig(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	cfg := config.Default()
	cfg.ProviderConfigured = false
	cfg.ProviderConfigurationKnown = true
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	m.SetLLMStatus(LLMUnconfigured, "")

	m = openFirstRunProviderSetup(t, m)
	m = pressKey(m, key("enter"))
	m = typeText(m, "sk-not-saved")
	m = pressKey(m, key("esc"))
	m = pressKey(m, key("esc"))

	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("global config written on cancel: %v", err)
	}
}

func TestProviderSetupEchoStaysOpenWhenGlobalConfigCannotBeWritten(t *testing.T) {
	homeFile := t.TempDir() + "/not-a-directory"
	if err := os.WriteFile(homeFile, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	setTestHome(t, homeFile)
	cfg := config.Default()
	cfg.ProviderConfigured = false
	cfg.ProviderConfigurationKnown = true
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	m.SetLLMStatus(LLMUnconfigured, "")
	m = openFirstRunProviderSetup(t, m)

	for range 4 {
		m = pressKey(m, key("down"))
	}
	m = pressKey(m, key("enter"))

	if m.state != stateProviderSetup {
		t.Fatalf("state = %v, want setup to remain open after write failure", m.state)
	}
	if cfg.ProviderConfigured {
		t.Fatal("failed echo save marked provider as configured")
	}
	if joined := plainText(strings.Join(m.blocks, "\n")); !strings.Contains(joined, "Failed to write global") {
		t.Fatalf("write failure not reported: %q", joined)
	}
}
