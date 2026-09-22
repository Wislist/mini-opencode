package agent

import (
	"strings"
	"testing"
)

// TestContextDetailsSplitsFixedFromConversation pins the distinction that made
// /newsession look broken: the system prompt is a large fixed floor, so the
// total barely moves when the conversation is cleared.
func TestContextDetailsSplitsFixedFromConversation(t *testing.T) {
	r := NewRuntime(EchoProvider{}, WithSystemPrompt(strings.Repeat("template text ", 400)))

	empty := r.ContextDetails()
	if empty.ConversationTokens != 0 {
		t.Fatalf("empty runtime reports conversation tokens: %+v", empty)
	}
	if empty.SystemTokens == 0 {
		t.Fatal("system prompt not counted")
	}
	if empty.Total() != empty.SystemTokens {
		t.Fatalf("total = %d, want the system prompt alone (%d)", empty.Total(), empty.SystemTokens)
	}

	r.SetMessages([]Message{{Role: RoleUser, Content: strings.Repeat("x", 400)}})
	withChat := r.ContextDetails()
	if withChat.ConversationTokens != 100 {
		t.Fatalf("conversation tokens = %d, want 100 for 400 ASCII chars", withChat.ConversationTokens)
	}
	if withChat.SystemTokens != empty.SystemTokens {
		t.Fatalf("system tokens changed with the transcript: %d -> %d",
			empty.SystemTokens, withChat.SystemTokens)
	}
	if withChat.Total() != withChat.SystemTokens+withChat.ConversationTokens {
		t.Fatalf("total = %d, want system+conversation", withChat.Total())
	}
}

// TestContextDetailsAfterReset asserts that clearing the transcript zeroes the
// conversation part while leaving the system prompt floor intact — the behavior
// /newsession depends on.
func TestContextDetailsAfterReset(t *testing.T) {
	r := NewRuntime(EchoProvider{}, WithSystemPrompt(strings.Repeat("template text ", 400)))
	r.SetMessages([]Message{{Role: RoleUser, Content: strings.Repeat("x", 400)}})
	r.SetContextTokens(50000)

	before := r.ContextDetails()
	if before.Reported != 50000 || before.Total() != 50000 {
		t.Fatalf("reported size not preferred: %+v total=%d", before, before.Total())
	}

	r.SetMessages(nil)
	r.SetContextTokens(0)
	after := r.ContextDetails()

	if after.ConversationTokens != 0 {
		t.Fatalf("conversation not cleared: %d", after.ConversationTokens)
	}
	if after.Reported != 0 {
		t.Fatalf("provider-reported size not cleared: %d", after.Reported)
	}
	if after.Total() != after.SystemTokens {
		t.Fatalf("total = %d, want the system prompt floor %d", after.Total(), after.SystemTokens)
	}
	if after.SystemTokens != before.SystemTokens {
		t.Fatalf("system floor changed across a reset: %d -> %d",
			before.SystemTokens, after.SystemTokens)
	}
}

// TestContextDetailsCountsOnlySentMessages asserts a compacted transcript does
// not report the retained-but-unsent history as context.
func TestContextDetailsCountsOnlySentMessages(t *testing.T) {
	r := NewRuntime(EchoProvider{}, WithContextWindow(100000), WithCompactionPrompt("summarize"))
	r.SetMessages([]Message{
		{Role: RoleUser, Content: strings.Repeat("old ", 200)},
		{Role: RoleAssistant, Content: strings.Repeat("old ", 200)},
	})
	before := r.ContextDetails().ConversationTokens
	if before == 0 {
		t.Fatal("conversation tokens not counted before compaction")
	}

	if _, err := r.CompactDetailed(t.Context(), "summarize"); err != nil {
		t.Fatalf("CompactDetailed() error = %v", err)
	}
	after := r.ContextDetails()
	// The retained history is no longer sent, so it must not be measured.
	if after.ConversationTokens >= before {
		t.Fatalf("conversation tokens did not shrink after compaction: %d -> %d",
			before, after.ConversationTokens)
	}
	if len(r.Messages()) <= 2 {
		t.Fatal("compaction dropped the retained history instead of truncating the prompt")
	}
}
