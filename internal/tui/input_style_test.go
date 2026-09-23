package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The prompt box must not paint a background anywhere: textarea's default
// CursorLine and EndOfBuffer styles fill the caret's whole row with a light
// background, which renders as a white bar across the input box. Reverse video
// on the caret itself is expected and is not asserted against.
var forbiddenInputBackgrounds = []string{
	"\x1b[40m", "\x1b[47m", "\x1b[44m", "\x1b[45m", "\x1b[48;5;", "\x1b[48;2;",
}

func assertNoInputBackground(t *testing.T, bar string) {
	t.Helper()
	for _, seq := range forbiddenInputBackgrounds {
		if strings.Contains(bar, seq) {
			t.Fatalf("input bar paints a background (%q): %q", seq, bar)
		}
	}
}

func TestInputBarPaintsNoBackground(t *testing.T) {
	m := New(nil, "/tmp", "test")
	m.width = 60
	m.height = 30

	assertNoInputBackground(t, m.renderInputBar())
}

// An empty input box is the common idle state and is where the stray fill in
// the row is most visible, so it gets its own case.
func TestInputBarEmptyPaintsNoBackground(t *testing.T) {
	m := New(nil, "/tmp", "test")
	m.width = 60
	m.height = 30
	m.input.SetValue("")

	bar := m.renderInputBar()
	assertNoInputBackground(t, bar)
	if got := ansi.Strip(bar); got == "" {
		t.Fatal("input bar rendered empty")
	}
}

// Typed text must still be visible after the styles are cleared.
func TestInputBarKeepsTypedText(t *testing.T) {
	m := New(nil, "/tmp", "test")
	m.width = 60
	m.height = 30
	m.input.SetValue("hello world")

	plain := ansi.Strip(m.renderInputBar())
	if !strings.Contains(plain, "hello world") {
		t.Fatalf("input bar lost typed text: %q", plain)
	}
}

// A terminal too short for the border falls back to a borderless row, which
// must clear the same backgrounds.
func TestInputBarBorderlessPaintsNoBackground(t *testing.T) {
	m := New(nil, "/tmp", "test")
	m.width = 60
	m.height = 1 // below minHeightForBorderedInput

	assertNoInputBackground(t, m.renderInputBar())
}
