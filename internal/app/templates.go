package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/agent/prompt"
	"github.com/wislist/mini-opencode/internal/config"
)

// initUserPrompt is the user turn paired with the initialize template. The
// template itself carries the full instruction set, so this stays short.
const initUserPrompt = "Analyze this repository and write the AGENTS.md file now."

// initSystemPrompt renders the initialize prompt template used by /init.
func initSystemPrompt(workingDir string) (string, error) {
	return prompt.BuildSystemPrompt(prompt.PromptInitialize, prompt.DefaultPromptContext(workingDir))
}

// titleGenerator produces a short session title from the first user message
// using the title prompt template.
type titleGenerator func(ctx context.Context, firstUserMessage string) (string, error)

// makeTitleGenerator builds a title generator bound to the live configuration.
//
// The config is taken by pointer on purpose: tilting happens lazily, long after
// startup, and /provider can have changed the provider in between. Capturing a
// copy would keep naming sessions with the provider the user has since left.
func makeTitleGenerator(cfg *config.Config, workingDir string) titleGenerator {
	return func(ctx context.Context, firstUserMessage string) (string, error) {
		provider, err := newProvider(cfg.Provider, workingDir)
		if err != nil {
			return "", err
		}
		system, err := prompt.RenderPromptTemplate(prompt.PromptTitle, prompt.DefaultPromptContext(workingDir))
		if err != nil {
			return "", err
		}
		resp, err := provider.Complete(ctx, agent.Request{
			SystemPrompt: system,
			Messages:     []agent.Message{{Role: agent.RoleUser, Content: firstUserMessage}},
		})
		if err != nil {
			return "", err
		}
		title := sanitizeSessionTitle(resp.Content)
		if title == "" {
			return "", fmt.Errorf("provider returned an empty title")
		}
		return title, nil
	}
}

// sanitizeSessionTitle keeps the model's answer usable as a one-line title:
// single line, no wrapping quotes, bounded length.
func sanitizeSessionTitle(raw string) string {
	title := strings.TrimSpace(raw)
	if idx := strings.IndexAny(title, "\r\n"); idx >= 0 {
		title = strings.TrimSpace(title[:idx])
	}
	title = strings.Trim(title, "\"'`“”‘’")
	title = strings.TrimSpace(title)
	const maxRunes = 50
	if runes := []rune(title); len(runes) > maxRunes {
		title = strings.TrimSpace(string(runes[:maxRunes]))
	}
	return title
}

// firstUserMessage returns the first real user turn of a conversation,
// skipping compaction summaries.
func firstUserMessage(messages []agent.Message) string {
	for _, msg := range messages {
		if msg.Role != agent.RoleUser {
			continue
		}
		if strings.Contains(msg.Content, "<conversation_summary>") {
			continue
		}
		if strings.Contains(msg.Content, "You are in plan mode") {
			// Strip the plan-mode preamble the UI prepends.
			if idx := strings.Index(msg.Content, "\n\n"); idx >= 0 {
				return strings.TrimSpace(msg.Content[idx+2:])
			}
		}
		return strings.TrimSpace(msg.Content)
	}
	return ""
}
