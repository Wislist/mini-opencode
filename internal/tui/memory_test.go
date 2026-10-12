package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/memory"
)

type tuiMemoryRetrieverStub struct {
	query string
	limit int
	notes []memory.Note
	err   error
}

func (r *tuiMemoryRetrieverStub) Search(_ context.Context, query string, limit int) ([]memory.Note, error) {
	r.query = query
	r.limit = limit
	return r.notes, r.err
}

func TestHandleMemoryRunsSearchAsCommandAndRendersResults(t *testing.T) {
	m := newTestModel(t)
	m.SetMemoryStore(memory.NewStore(t.TempDir()))
	retriever := &tuiMemoryRetrieverStub{notes: []memory.Note{{
		Name:  "release",
		Title: "Release flow",
		Tags:  []string{"ops"},
		Body:  "Promote staging after smoke tests.",
	}}}
	m.SetMemoryRetriever(retriever)

	_, cmd := m.handleMemory("/memory shipping")
	if cmd == nil {
		t.Fatal("handleMemory() returned nil command")
	}
	if len(m.blocks) != 0 {
		t.Fatalf("search rendered synchronously: blocks = %#v", m.blocks)
	}
	msg, ok := cmd().(memorySearchMsg)
	if !ok {
		t.Fatalf("command message type = %T, want memorySearchMsg", cmd())
	}
	if retriever.query != "shipping" || retriever.limit != memoryListLimit {
		t.Fatalf("Search() args = (%q, %d)", retriever.query, retriever.limit)
	}
	m.Update(msg)

	got := plainText(strings.Join(m.blocks, "\n"))
	for _, want := range []string{"memory (1 note(s)", "Release flow", "tags: ops", "Promote staging"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered memory missing %q: %q", want, got)
		}
	}
	if m.transcriptDirty {
		t.Fatal("memory result left viewport transcript dirty")
	}
}

func TestMemorySearchMessageRendersEmptyAndErrorStates(t *testing.T) {
	tests := []struct {
		name string
		msg  memorySearchMsg
		want string
	}{
		{name: "empty list", msg: memorySearchMsg{}, want: "no memories stored yet"},
		{name: "no match", msg: memorySearchMsg{query: "missing"}, want: `no memories matched "missing"`},
		{name: "error", msg: memorySearchMsg{err: errors.New("offline")}, want: "memory: offline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			m.SetMemoryStore(memory.NewStore(t.TempDir()))
			m.Update(tt.msg)
			got := plainText(strings.Join(m.blocks, "\n"))
			if !strings.Contains(got, tt.want) {
				t.Fatalf("rendered = %q, want substring %q", got, tt.want)
			}
			if m.transcriptDirty {
				t.Fatal("memory result left viewport transcript dirty")
			}
		})
	}
}
