package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubCompleter returns a canned response or error.
type stubCompleter struct {
	response string
	err      error
	// sawPrompt and sawMessages record what the distiller sent.
	sawPrompt   string
	sawMessages []DistillMessage
}

func (s *stubCompleter) Complete(_ context.Context, systemPrompt string, messages []DistillMessage) (string, error) {
	s.sawPrompt = systemPrompt
	s.sawMessages = messages
	return s.response, s.err
}

func TestDistillExtractsNotes(t *testing.T) {
	store := NewStore(t.TempDir())
	completer := &stubCompleter{response: `[
		{"name":"WAL Mode","title":"SQLite WAL","tags":["Database"],"body":"WAL keeps readers from blocking writers."},
		{"name":"viewport-math","title":"Viewport","tags":[],"body":"Width is reduced by one."}
	]`}
	d := NewDistiller(completer, store)

	result, err := d.Extract(context.Background(), []DistillMessage{
		{Role: "user", Content: "why does the UI scroll"},
		{Role: "assistant", Content: "because of the last cell"},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Notes) != 2 {
		t.Fatalf("notes = %d, want 2 (%#v)", len(result.Notes), result.Notes)
	}
	// Names must be slugified on the way in.
	if result.Notes[0].Name != "wal-mode" {
		t.Fatalf("name = %q, want slugified", result.Notes[0].Name)
	}
	// The notes must be persisted, not merely returned.
	stored, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored = %d, want 2", len(stored))
	}
}

// TestDistillToleratesWrappedJSON covers the shapes models actually emit even
// when asked for bare JSON.
func TestDistillToleratesWrappedJSON(t *testing.T) {
	cases := map[string]string{
		"fenced":                  "```json\n[{\"name\":\"a\",\"body\":\"x\"}]\n```",
		"bare fence":              "```\n[{\"name\":\"a\",\"body\":\"x\"}]\n```",
		"leading text":            "Here are the notes:\n[{\"name\":\"a\",\"body\":\"x\"}]\nHope that helps!",
		"trailing comma in prose": "Sure! [{\"name\":\"a\",\"body\":\"x\"}] done",
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			store := NewStore(t.TempDir())
			d := NewDistiller(&stubCompleter{response: response}, store)
			result, err := d.Extract(context.Background(), []DistillMessage{{Role: "user", Content: "hi"}})
			if err != nil {
				t.Fatalf("Extract() error = %v", err)
			}
			if len(result.Notes) != 1 {
				t.Fatalf("notes = %d, want 1 (raw=%q)", len(result.Notes), response)
			}
		})
	}
}

func TestDistillEmptyArrayIsNotAnError(t *testing.T) {
	store := NewStore(t.TempDir())
	d := NewDistiller(&stubCompleter{response: "[]"}, store)
	result, err := d.Extract(context.Background(), []DistillMessage{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("notes = %#v, want none", result.Notes)
	}
	if notes, _ := store.List(); len(notes) != 0 {
		t.Fatalf("stored = %#v, want none", notes)
	}
}

func TestDistillUnparseableResponseIsNotFatal(t *testing.T) {
	store := NewStore(t.TempDir())
	d := NewDistiller(&stubCompleter{response: "I could not find anything to remember."}, store)
	result, err := d.Extract(context.Background(), []DistillMessage{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Extract() error = %v, want nil for a soft failure", err)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("notes = %#v", result.Notes)
	}
	if result.Raw == "" {
		t.Fatal("raw response was not preserved for diagnostics")
	}
}

func TestDistillSkipsInvalidNotes(t *testing.T) {
	store := NewStore(t.TempDir())
	completer := &stubCompleter{response: `[
		{"name":"good","body":"valid"},
		{"name":"empty-body","body":"   "},
		{"name":"!!!","title":"","body":"unslugifiable"}
	]`}
	d := NewDistiller(completer, store)
	result, err := d.Extract(context.Background(), []DistillMessage{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Notes) != 1 {
		t.Fatalf("notes = %d, want only the valid one", len(result.Notes))
	}
	if result.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (the empty body)", result.Skipped)
	}
}

func TestDistillPropagatesProviderError(t *testing.T) {
	store := NewStore(t.TempDir())
	d := NewDistiller(&stubCompleter{err: errors.New("boom")}, store)
	if _, err := d.Extract(context.Background(), []DistillMessage{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("Extract() error = nil, want the provider error")
	}
}

func TestDistillNilAndEmptyInputs(t *testing.T) {
	var d *Distiller
	if _, err := d.Extract(context.Background(), []DistillMessage{{Role: "user", Content: "x"}}); err != nil {
		t.Fatalf("nil distiller error = %v", err)
	}
	store := NewStore(t.TempDir())
	real := NewDistiller(&stubCompleter{response: "[]"}, store)
	if _, err := real.Extract(context.Background(), nil); err != nil {
		t.Fatalf("empty input error = %v", err)
	}
	if NewDistiller(nil, store) != nil {
		t.Fatal("distiller built without a completer")
	}
	if NewDistiller(&stubCompleter{}, nil) != nil {
		t.Fatal("distiller built without a store")
	}
}

// TestDistillCapsTranscriptLength keeps the extra provider call bounded.
func TestDistillCapsTranscriptLength(t *testing.T) {
	store := NewStore(t.TempDir())
	completer := &stubCompleter{response: "[]"}
	d := NewDistiller(completer, store)

	var messages []DistillMessage
	for i := 0; i < maxDistillMessages+25; i++ {
		messages = append(messages, DistillMessage{Role: "user", Content: "message"})
	}
	if _, err := d.Extract(context.Background(), messages); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(completer.sawMessages) != maxDistillMessages {
		t.Fatalf("sent %d messages, want the cap of %d", len(completer.sawMessages), maxDistillMessages)
	}
}

// TestDistillPromptForbidsSecrets guards the instruction that keeps credentials
// out of on-disk memory.
func TestDistillPromptForbidsSecrets(t *testing.T) {
	lower := strings.ToLower(distillPrompt)
	for _, want := range []string{"secret", "credential", "api key"} {
		if !strings.Contains(lower, want) {
			t.Errorf("distill prompt does not mention %q", want)
		}
	}
	if !strings.Contains(lower, "do not record") {
		t.Error("distill prompt has no exclusions")
	}
}

func TestIsCompactionSummary(t *testing.T) {
	if !IsCompactionSummary("<conversation_summary>\nstuff") {
		t.Fatal("summary not detected")
	}
	if IsCompactionSummary("just a normal message") {
		t.Fatal("false positive")
	}
}
