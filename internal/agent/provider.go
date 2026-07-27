package agent

import (
	"context"
	"fmt"
	"strings"
)

type Provider interface {
	Complete(ctx context.Context, req Request) (AssistantResponse, error)
	// CompleteStream streams the assistant response, invoking onDelta for each
	// content token chunk as it arrives. It returns the full accumulated
	// AssistantResponse when the stream finishes. Providers that do not
	// support streaming may emit the whole content in a single delta.
	CompleteStream(ctx context.Context, req Request, onDelta func(string)) (AssistantResponse, error)
}

type Request struct {
	SystemPrompt string
	Messages     []Message
	Tools        []ToolSpec
}

type EchoProvider struct{}

func (p EchoProvider) Complete(ctx context.Context, req Request) (AssistantResponse, error) {
	select {
	case <-ctx.Done():
		return AssistantResponse{}, ctx.Err()
	default:
	}

	last := lastUserMessage(req.Messages)
	if last == "" {
		return AssistantResponse{Content: "ready"}, nil
	}
	return AssistantResponse{
		Content: fmt.Sprintf("runtime ready. received: %s", last),
	}, nil
}

// CompleteStream emits the full response as a single delta, since the echo
// provider has no real streaming source.
func (p EchoProvider) CompleteStream(ctx context.Context, req Request, onDelta func(string)) (AssistantResponse, error) {
	resp, err := p.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if onDelta != nil && resp.Content != "" {
		onDelta(resp.Content)
	}
	return resp, nil
}

func lastUserMessage(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			return strings.TrimSpace(messages[i].Content)
		}
	}
	return ""
}
