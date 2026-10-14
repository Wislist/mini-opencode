package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/config"
)

type providerSetupOption struct {
	label   string
	summary string
	entry   config.ProviderConfig
	custom  bool
}

func providerSetupCatalog() []providerSetupOption {
	return []providerSetupOption{
		{
			label:   "DeepSeek",
			summary: "OpenAI-compatible · api.deepseek.com",
			entry: config.ProviderConfig{
				Name: "deepseek", Label: "DeepSeek", BaseURL: "https://api.deepseek.com", APIKeyEnv: "DEEPSEEK_API_KEY",
			},
		},
		{
			label:   "OpenAI",
			summary: "OpenAI-compatible · api.openai.com",
			entry: config.ProviderConfig{
				Name: "openai", Label: "OpenAI", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY",
			},
		},
		{
			label:   "OpenRouter",
			summary: "OpenAI-compatible · openrouter.ai",
			entry: config.ProviderConfig{
				Name: "openrouter", Label: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY",
			},
		},
		{
			label:   "Custom OpenAI-compatible",
			summary: "enter a name, API URL, and API key",
			custom:  true,
		},
		{
			label:   "Local Echo",
			summary: "no key required · local echo and UI testing only",
			entry:   config.ProviderConfig{Name: "echo", Label: "Local Echo", Type: config.ProviderTypeEcho},
		},
	}
}

func (m *Model) openProviderSetup() {
	m.providerSetup = providerSetupCatalog()
	m.providerSetupCursor = 0
	m.pendingAdd = nil
	m.state = stateProviderSetup
	m.invalidateFooter()
	m.refreshViewport()
}

func (m *Model) closeProviderSetup() (tea.Model, tea.Cmd) {
	m.providerSetup = nil
	m.providerSetupCursor = 0
	m.state = stateIdle
	m.invalidateFooter()
	m.refreshViewport()
	return m, m.input.Focus()
}

func (m *Model) handleProviderSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "shift+tab":
		if m.providerSetupCursor > 0 {
			m.providerSetupCursor--
		}
	case "down", "j", "tab":
		if m.providerSetupCursor < len(m.providerSetup)-1 {
			m.providerSetupCursor++
		}
	case "enter":
		if m.providerSetupCursor >= len(m.providerSetup) {
			return m.closeProviderSetup()
		}
		return m.applyProviderSetup(m.providerSetup[m.providerSetupCursor])
	case "esc", "ctrl+c", "q":
		return m.closeProviderSetup()
	default:
		return m, nil
	}
	m.invalidateFooter()
	m.refreshViewport()
	return m, nil
}

func (m *Model) applyProviderSetup(option providerSetupOption) (tea.Model, tea.Cmd) {
	if option.custom {
		m.openProviderForm()
		m.providerForm.returnToSetup = true
		return m, nil
	}
	if option.entry.EffectiveType() == config.ProviderTypeEcho {
		m.pendingAdd = &pendingProviderAdd{provider: option.entry}
		if !m.savePendingProvider(nil) {
			m.pendingAdd = nil
			m.invalidateFooter()
			m.refreshViewport()
			return m, nil
		}
		m.providerSetup = nil
		m.state = stateIdle
		m.invalidateFooter()
		m.refreshViewport()
		return m, m.input.Focus()
	}
	m.openKnownProviderForm(option.entry, true)
	return m, nil
}

func (m *Model) renderProviderSetup() string {
	w := boxWidth(m)
	inner := max(1, w-4)
	start, end := visibleMenuWindow(len(m.providerSetup), m.providerSetupCursor, m.menuItemBudget())
	lines := []string{
		keyLabel.Render("Configure a model provider:") + "  " + dimStyle.Render("↑↓ select · enter confirm · esc configure later"),
	}
	if label := windowLabel(start, end, len(m.providerSetup)); label != "" {
		lines = append(lines, dimStyle.Render(label))
	}
	for i := start; i < end; i++ {
		item := m.providerSetup[i]
		cursor := "  "
		if i == m.providerSetupCursor {
			cursor = "▶ "
		}
		line := fmt.Sprintf("%s%s", cursor, toolLabelStyle(i == m.providerSetupCursor, item.label))
		if item.summary != "" {
			line += dimStyle.Render("  " + item.summary)
		}
		lines = append(lines, line)
	}
	lines = append(lines, dimStyle.Render("Keys and provider settings are stored in global ~/.mini-opencode/config.json"))
	return sessionBox.Width(w).Render(strings.Join(m.wrapMenuRows(lines, inner, 1+(end-start)), "\n"))
}
