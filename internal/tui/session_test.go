package tui

import (
	"testing"

	"github.com/wislist/mini-opencode/internal/config"
)

func TestHandleListSessionsDoesNotAddLoadingBlock(t *testing.T) {
	m := New(&config.Config{}, t.TempDir(), "test")
	m.SetSessionStore(newTestSessionStore(t, t.TempDir()))
	m.addBlock("existing transcript")

	before := append([]string(nil), m.blocks...)
	_, cmd := m.handleListSessions()
	if cmd == nil {
		t.Fatal("session list did not return an asynchronous load command")
	}
	if len(m.blocks) != len(before) || m.blocks[0] != before[0] {
		t.Fatalf("session list changed the transcript: before=%q after=%q", before, m.blocks)
	}
}
