package tui

import (
	"errors"
	"strings"
	"testing"
)

// undoTranscript joins the rendered blocks so an assertion does not depend on
// which block index the message landed in.
func undoTranscript(m *Model) string {
	return strings.Join(m.blocks, "\n")
}

func TestHandleUndoWithoutRestorerReportsNotConfigured(t *testing.T) {
	m := newTestModel(t)

	if _, cmd := m.handleUndo(); cmd != nil {
		t.Fatal("handleUndo() returned a command with no restorer")
	}
	if got := undoTranscript(m); !strings.Contains(got, "snapshot restore is not configured") {
		t.Fatalf("transcript = %q", got)
	}
}

func TestHandleUndoPropagatesRestorerError(t *testing.T) {
	m := newTestModel(t)
	m.SetSnapshotRestorer(func() (string, error) {
		return "", errors.New("disk on fire")
	})

	if _, cmd := m.handleUndo(); cmd != nil {
		t.Fatal("handleUndo() returned a command on the error path")
	}
	got := undoTranscript(m)
	if !strings.Contains(got, "undo: disk on fire") {
		t.Fatalf("transcript = %q", got)
	}
	if strings.Contains(got, "restored ") {
		t.Fatalf("error path reported success: %q", got)
	}
}

func TestHandleUndoReportsRestoredPath(t *testing.T) {
	m := newTestModel(t)
	m.SetSnapshotRestorer(func() (string, error) {
		return "demo.txt", nil
	})

	if _, cmd := m.handleUndo(); cmd != nil {
		t.Fatal("handleUndo() returned a command on the success path")
	}
	if got := undoTranscript(m); !strings.Contains(got, "restored demo.txt") {
		t.Fatalf("transcript = %q", got)
	}
}

// Pressing /undo through the input path must reach handleUndo, so the command
// stays wired to the dispatch switch rather than only to the callback.
func TestUndoInputDispatchesToHandler(t *testing.T) {
	m := newTestModel(t)
	m.SetSnapshotRestorer(func() (string, error) {
		return "demo.txt", nil
	})

	m.handleInput("/undo")

	if got := undoTranscript(m); !strings.Contains(got, "restored demo.txt") {
		t.Fatalf("transcript = %q", got)
	}
}
