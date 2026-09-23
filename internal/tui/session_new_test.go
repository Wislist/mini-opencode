package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"

	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/session"
)

func TestHandleNewSessionClearsTranscriptAndScreen(t *testing.T) {
	cfg := config.Default()
	m := &Model{
		viewport:      viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		cfg:           &cfg,
		sessions:      session.NewStore(t.TempDir()),
		streamingIdx:  3,
		streamingText: "partial response",
		blocks:        []string{"old user message", "old assistant message"},
	}
	m.refreshViewport()

	_, cmd := m.handleNewSession()

	if cmd == nil {
		t.Fatal("handleNewSession() returned no clear-screen command")
	}
	if len(m.blocks) != 1 || strings.Contains(m.blocks[0], "old") {
		t.Fatalf("blocks after new session = %#v, want only fresh-session status", m.blocks)
	}
	if m.streamingIdx != -1 || m.streamingText != "" {
		t.Fatalf("streaming state not reset: index=%d text=%q", m.streamingIdx, m.streamingText)
	}
	if m.viewport.YOffset() != 0 {
		t.Fatalf("viewport offset = %d, want 0", m.viewport.YOffset())
	}
	if got := m.viewport.View(); strings.Contains(got, "old user message") || strings.Contains(got, "old assistant message") {
		t.Fatalf("viewport still contains the previous transcript: %q", got)
	}
}
