package tui

import (
	"context"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

// titleGenerator produces a short session title from the first user message.
// The app layer implements it with the configured provider and the title
// prompt template.
type titleGenerator func(ctx context.Context, firstUserMessage string) (string, error)

// initUserPrompt is the user turn paired with the initialize prompt template.
const initUserPrompt = "Analyze this repository and write the AGENTS.md file now."

// firstUserMessage returns the first real user turn, skipping compaction
// summaries and the plan-mode preamble the UI prepends.
func firstUserMessage(messages []agent.Message) string {
	for _, msg := range messages {
		if msg.Role != agent.RoleUser {
			continue
		}
		if strings.Contains(msg.Content, "<conversation_summary>") {
			continue
		}
		if strings.Contains(msg.Content, "You are in plan mode") {
			if idx := strings.Index(msg.Content, "\n\n"); idx >= 0 {
				return strings.TrimSpace(msg.Content[idx+2:])
			}
		}
		return strings.TrimSpace(msg.Content)
	}
	return ""
}
