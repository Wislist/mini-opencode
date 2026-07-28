package tui

import (
	"bytes"
	"io"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/wislist/mini-opencode/internal/config"
)

func TestNativeCursorPositionTracksIdleInputCursor(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24}); cmd != nil {
		t.Fatal("WindowSizeMsg returned an unexpected command")
	}
	m.input.SetValue("abc")
	m.input.CursorEnd()

	_ = m.View()

	col, row, ok := m.NativeCursorPosition()
	if !ok {
		t.Fatal("NativeCursorPosition ok = false")
	}
	// Width is reserved as terminal width - 1. The common footer is a three-line
	// input box plus one help line, so the input content row is row 22.
	if row != 22 {
		t.Fatalf("row = %d, want 22", row)
	}
	// left border + padding + "❯ " + "abc", then the 1-based cursor cell.
	if col != 8 {
		t.Fatalf("col = %d, want 8", col)
	}
}

func TestNativeCursorPositionKeepsInputCursorStableWithCommandMenu(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24}); cmd != nil {
		t.Fatal("WindowSizeMsg returned an unexpected command")
	}
	m.input.SetValue("/s")
	m.updateCommandMenu()
	if len(m.commandFiltered) == 0 {
		t.Fatal("command menu did not open")
	}

	_ = m.View()

	_, row, ok := m.NativeCursorPosition()
	if !ok {
		t.Fatal("NativeCursorPosition ok = false")
	}
	// The command menu grows upward by shrinking the viewport, so the input row
	// remains stable at the bottom of the terminal.
	if row != 22 {
		t.Fatalf("row = %d, want 22", row)
	}
}

func TestNativeCursorWriterPreservesTerminalFileInterface(t *testing.T) {
	f := &fakeTerminalFile{Buffer: &bytes.Buffer{}, fd: 123}
	w := NewNativeCursorWriter(f, nil)
	tf, ok := w.(terminalFile)
	if !ok {
		t.Fatal("wrapped terminal file no longer implements terminalFile")
	}
	if got := tf.Fd(); got != 123 {
		t.Fatalf("Fd() = %d, want 123", got)
	}
}

type fakeTerminalFile struct {
	*bytes.Buffer
	fd uintptr
}

func (f *fakeTerminalFile) Read(p []byte) (int, error) { return 0, io.EOF }
func (f *fakeTerminalFile) Close() error               { return nil }
func (f *fakeTerminalFile) Fd() uintptr                { return f.fd }

func TestNativeCursorWriterAppendsCursorMoveAfterRenderCursor(t *testing.T) {
	var out bytes.Buffer
	w := NewNativeCursorWriter(&out, func() (int, int, bool) { return 5, 10, true })

	if n, err := w.Write([]byte("frame\x1b[24;H")); err != nil || n != len("frame\x1b[24;H") {
		t.Fatalf("Write n=%d err=%v", n, err)
	}

	want := "frame\x1b[24;H" + ansi.SetTextCursorEnableMode + ansi.CursorPosition(5, 10)
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestNativeCursorWriterHidesCursorWhenNoInputIsFocused(t *testing.T) {
	var out bytes.Buffer
	w := NewNativeCursorWriter(&out, func() (int, int, bool) { return 0, 0, false })

	if _, err := w.Write([]byte("frame\x1b[24;H")); err != nil {
		t.Fatal(err)
	}

	want := "frame\x1b[24;H" + ansi.ResetTextCursorEnableMode
	if got := out.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestNativeCursorWriterLeavesOtherWritesAlone(t *testing.T) {
	var out bytes.Buffer
	w := NewNativeCursorWriter(&out, func() (int, int, bool) { return 5, 10, true })

	if _, err := w.Write([]byte("\x1b[?1007h")); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "\x1b[?1007h"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
