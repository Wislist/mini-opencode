package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/wislist/mini-opencode/internal/config"
)

// llmHeaderLabel keeps the fallback echo implementation from masquerading as
// a configured model while still naming a provider that exists but cannot be
// started.
func (m *Model) llmHeaderLabel() string {
	switch m.llmStatus {
	case LLMUnconfigured:
		return "No model configured"
	case LLMUnavailable:
		name := strings.TrimSpace(m.cfg.Provider.DisplayName())
		if name == "" {
			name = "provider"
		}
		return name + " · disconnected"
	default:
		return m.cfg.Provider.Name
	}
}

// renderLLMEmptyState renders first-run and disconnected guidance directly in
// the viewport area. It deliberately does not append to blocks, so the notice
// is not persisted, copied into a session, or sent back to a future provider.
func (m *Model) renderLLMEmptyState() string {
	var title, description string
	switch m.llmStatus {
	case LLMUnavailable:
		name := strings.TrimSpace(m.cfg.Provider.DisplayName())
		if name == "" {
			name = "Provider"
		}
		title = fmt.Sprintf("%s is unavailable", name)
		description = "Update the provider configuration to continue this conversation."
	default:
		title = "No LLM configured"
		description = "Configure a provider to start a conversation."
	}

	contentWidth := max(1, min(48, m.width-4))
	lines := []string{
		toolName.Render(title),
		"",
		dimStyle.Render(wordWrap(description, contentWidth)),
		"",
		keyLabel.Render("Enter") + dimStyle.Render("  configure a provider"),
		dimStyle.Render("or type /provider add"),
	}
	content := strings.Join(lines, "\n")
	return lipgloss.Place(
		max(1, m.width),
		max(1, m.viewport.Height()),
		lipgloss.Center,
		lipgloss.Center,
		content,
	)
}

// openProviderRecoveryForm starts first-run provider selection, or asks only
// for the missing key when an existing remote provider cannot start. The main
// composer remains untouched so a send attempt can be resumed after setup.
func (m *Model) openProviderRecoveryForm() {
	if m.llmStatus != LLMUnavailable || m.cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
		m.openProviderSetup()
		return
	}
	m.openKnownProviderForm(m.cfg.Provider, false)
}
