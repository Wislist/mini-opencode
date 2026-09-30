package agent

import (
	"context"
	"strings"
	"testing"
)

func TestAdoptStateFromMovesConversation(t *testing.T) {
	src := NewRuntime(EchoProvider{}, WithSystemPrompt("system prompt A"))
	src.SetMessages([]Message{
		{Role: RoleUser, Content: "first"},
		{Role: RoleAssistant, Content: "answer"},
	})
	src.SetUsage(Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150})
	src.SetContextTokens(4096)

	dst := NewRuntime(EchoProvider{}, WithSystemPrompt("system prompt B"))
	dst.AdoptStateFrom(src)

	msgs := dst.Messages()
	if len(msgs) != 2 || msgs[0].Content != "first" || msgs[1].Content != "answer" {
		t.Fatalf("Messages() = %+v, want the source transcript", msgs)
	}
	if got := dst.Usage(); got.TotalTokens != 150 || got.PromptTokens != 120 {
		t.Fatalf("Usage() = %+v, want the source usage", got)
	}
	if got := dst.ContextTokens(); got != 4096 {
		t.Fatalf("ContextTokens() = %d, want 4096", got)
	}
	if got := dst.SystemPrompt(); got != "system prompt A" {
		t.Fatalf("SystemPrompt() = %q, want the source prompt", got)
	}
}

// The copy has to be deep: the source runtime keeps running (or gets dropped)
// while the new one serves the conversation.
func TestAdoptStateFromCopiesTheTranscript(t *testing.T) {
	src := NewRuntime(EchoProvider{})
	src.SetMessages([]Message{{Role: RoleUser, Content: "original"}})

	dst := NewRuntime(EchoProvider{})
	dst.AdoptStateFrom(src)

	src.SetMessages([]Message{{Role: RoleUser, Content: "replaced"}})
	if got := dst.Messages()[0].Content; got != "original" {
		t.Fatalf("destination transcript changed with the source: %q", got)
	}
}

// A compacted transcript must stay compacted: the marker is re-derived from the
// content, so a provider switch cannot resurrect the pre-summary history.
func TestAdoptStateFromKeepsCompactionMarker(t *testing.T) {
	src := NewRuntime(EchoProvider{}, WithCompactionPrompt("summarize"))
	src.SetMessages([]Message{
		{Role: RoleUser, Content: "old question"},
		{Role: RoleAssistant, Content: "<conversation_summary>summary</conversation_summary>"},
		{Role: RoleUser, Content: "new question"},
	})

	dst := NewRuntime(EchoProvider{}, WithCompactionPrompt("summarize"))
	dst.AdoptStateFrom(src)

	_, sent := dst.promptState()
	if len(sent) != 2 {
		t.Fatalf("promptState sent %d messages, want 2 (truncated at the summary)", len(sent))
	}
	if sent[0].Role != RoleUser {
		t.Fatalf("first sent message role = %q, want user", sent[0].Role)
	}
	if !strings.Contains(sent[0].Content, "summary") {
		t.Fatalf("first sent message = %q, want the summary", sent[0].Content)
	}
}

// Boundary: nil and self are no-ops rather than panics.
func TestAdoptStateFromEdgeCases(t *testing.T) {
	var nilRuntime *Runtime
	rt := NewRuntime(EchoProvider{})
	rt.SetMessages([]Message{{Role: RoleUser, Content: "keep"}})

	nilRuntime.AdoptStateFrom(rt)

	rt.AdoptStateFrom(nil)
	if len(rt.Messages()) != 1 {
		t.Fatalf("messages changed on a nil source: %+v", rt.Messages())
	}

	rt.AdoptStateFrom(rt)
	if len(rt.Messages()) != 1 || rt.Messages()[0].Content != "keep" {
		t.Fatalf("self-adoption damaged the transcript: %+v", rt.Messages())
	}
}

// The adopted runtime must be usable, not just populated: the conversation
// continues against the new provider.
func TestAdoptedRuntimeContinuesTheConversation(t *testing.T) {
	src := NewRuntime(EchoProvider{})
	src.SetMessages([]Message{{Role: RoleUser, Content: "hello"}})

	dst := NewRuntime(EchoProvider{})
	dst.AdoptStateFrom(src)

	if err := dst.Run(context.Background(), "next", func(Event) {}); err != nil {
		t.Fatalf("Run on the adopted runtime: %v", err)
	}
	last := dst.Messages()[len(dst.Messages())-1]
	if last.Role != RoleAssistant {
		t.Fatalf("last message role = %q, want assistant", last.Role)
	}
}
