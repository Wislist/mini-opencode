package app

import (
	"context"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/memory"
)

// distillCompleter adapts an agent.Provider to memory.Completer, so the
// distiller can reuse the configured provider without the memory package
// depending on the agent package.
type distillCompleter struct {
	provider agent.Provider
}

// Complete sends one non-streaming completion. Distillation is a background
// task whose output is parsed rather than shown, so there is nothing to stream.
func (c distillCompleter) Complete(ctx context.Context, systemPrompt string, messages []memory.DistillMessage) (string, error) {
	history := make([]agent.Message, 0, len(messages))
	for _, msg := range messages {
		history = append(history, agent.Message{Role: agent.Role(msg.Role), Content: msg.Content})
	}
	resp, err := c.provider.Complete(ctx, agent.Request{
		SystemPrompt: systemPrompt,
		Messages:     history,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// newDistiller builds the automatic memory extractor for a workspace, or nil
// when the provider cannot be constructed. Callers treat nil as "no automatic
// extraction" rather than an error: remembering things is an enhancement, and
// a workspace without a working provider can still be used.
func newDistiller(provider agent.Provider, store *memory.Store) *memory.Distiller {
	if provider == nil || store == nil {
		return nil
	}
	return memory.NewDistiller(distillCompleter{provider: provider}, store)
}
