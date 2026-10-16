package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

type stubReviewProvider struct {
	requests []agent.Request
	response agent.AssistantResponse
	err      error
}

func (s *stubReviewProvider) Complete(_ context.Context, req agent.Request) (agent.AssistantResponse, error) {
	s.requests = append(s.requests, req)
	return s.response, s.err
}

func TestCommandReviewerApprovesAndDenies(t *testing.T) {
	for _, tc := range []struct {
		name        string
		content     string
		wantAllowed bool
		wantReason  string
	}{
		{"approved", `{"allow":true,"reason":"runs the project tests"}`, true, "runs the project tests"},
		{"denied", `{"allow":false,"reason":"writes outside the workspace"}`, false, "writes outside the workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &stubReviewProvider{response: agent.AssistantResponse{Content: tc.content}}
			reviewer := &commandReviewer{provider: provider, systemPrompt: func() (string, error) { return "review the command", nil }}

			allowed, reason := reviewer.ReviewCommand(context.Background(), "make deploy")
			if allowed != tc.wantAllowed || reason != tc.wantReason {
				t.Fatalf("got (%v, %q) want (%v, %q)", allowed, reason, tc.wantAllowed, tc.wantReason)
			}
			if len(provider.requests) != 1 {
				t.Fatalf("provider calls = %d", len(provider.requests))
			}
			req := provider.requests[0]
			if req.SystemPrompt != "review the command" || len(req.Tools) != 0 {
				t.Fatalf("request = %+v", req)
			}
			if len(req.Messages) != 1 || req.Messages[0].Role != agent.RoleUser || !strings.Contains(req.Messages[0].Content, "make deploy") {
				t.Fatalf("messages = %+v", req.Messages)
			}
		})
	}
}

func TestCommandReviewerFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider *stubReviewProvider
		prompt   func() (string, error)
	}{
		{"provider error", &stubReviewProvider{err: errors.New("dial tcp: refused")}, nil},
		{"unparseable reply", &stubReviewProvider{response: agent.AssistantResponse{Content: "probably fine?"}}, nil},
		{"prompt failure", &stubReviewProvider{}, func() (string, error) { return "", errors.New("missing template") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			systemPrompt := tc.prompt
			if systemPrompt == nil {
				systemPrompt = func() (string, error) { return "review the command", nil }
			}
			reviewer := &commandReviewer{provider: tc.provider, systemPrompt: systemPrompt}

			allowed, reason := reviewer.ReviewCommand(context.Background(), "make deploy")
			if allowed {
				t.Fatalf("reviewer approved despite %s", tc.name)
			}
			if strings.TrimSpace(reason) == "" {
				t.Fatalf("reviewer gave no reason for %s", tc.name)
			}
		})
	}
}

func TestCommandReviewerRendersTheRealPrompt(t *testing.T) {
	reviewer, ok := newCommandReviewer(&stubReviewProvider{}, t.TempDir()).(*commandReviewer)
	if !ok {
		t.Fatal("newCommandReviewer did not return a command reviewer")
	}
	systemPrompt, err := reviewer.system()
	if err != nil {
		t.Fatalf("system prompt error = %v", err)
	}
	if !strings.Contains(systemPrompt, "allow") {
		t.Fatalf("system prompt does not describe the verdict field: %q", systemPrompt)
	}
}
