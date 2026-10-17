package agent

import (
	"strings"
	"testing"
)

// AppendInterruptedAssistant keeps what the model had already streamed when the
// user interrupted the run. Without it the next request has no idea how far the
// previous answer got, which is what makes "interrupt and adjust the goal"
// restart from nothing.

func TestAppendInterruptedAssistantRecordsPartial(t *testing.T) {
	r := NewRuntime(EchoProvider{})
	r.AppendInterruptedAssistant("half an answer")

	msgs := r.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	if msgs[0].Role != RoleAssistant {
		t.Fatalf("role = %v, want assistant", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "half an answer") {
		t.Fatalf("partial content missing: %q", msgs[0].Content)
	}
}

func TestAppendInterruptedAssistantIgnoresEmpty(t *testing.T) {
	r := NewRuntime(EchoProvider{})
	for _, content := range []string{"", "   ", "\n\t\n"} {
		r.AppendInterruptedAssistant(content)
	}
	if got := len(r.Messages()); got != 0 {
		t.Fatalf("messages = %d, want 0 for empty content", got)
	}
}
