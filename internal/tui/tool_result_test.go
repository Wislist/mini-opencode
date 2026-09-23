package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/wislist/mini-opencode/internal/agent"
)

func toolFinishedModel(t *testing.T, name, content string) *Model {
	t.Helper()
	m := New(nil, "/tmp", "test")
	m.width = 60
	m.height = 30
	m.Update(runtimeEventMsg{event: agent.Event{
		Type:       agent.EventToolCallFinished,
		ToolResult: &agent.ToolResult{Name: name, Content: content},
	}})
	return m
}

func lastBlockText(m *Model) string {
	if len(m.blocks) == 0 {
		return ""
	}
	return m.blocks[len(m.blocks)-1]
}

// Tool output is content the user reads, so it is boxed like the tool call
// itself instead of being emitted as a bare "→ ..." line.
func TestToolResultRendersInBox(t *testing.T) {
	m := toolFinishedModel(t, "bash", "line one\nline two\nline three")

	block := lastBlockText(m)
	plain := ansi.Strip(block)
	if !strings.Contains(plain, "╭") || !strings.Contains(plain, "╰") {
		t.Fatalf("tool result is not boxed: %q", plain)
	}
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("tool result dropped %q: %q", want, plain)
		}
	}
}

// Multi-line output must keep its line structure rather than being collapsed
// onto one row.
func TestToolResultKeepsLineBreaks(t *testing.T) {
	m := toolFinishedModel(t, "read", "alpha\nbeta")

	plain := ansi.Strip(lastBlockText(m))
	if strings.Contains(plain, "alpha beta") {
		t.Fatalf("tool result collapsed its newlines: %q", plain)
	}
	lines := strings.Split(plain, "\n")
	if len(lines) < 3 {
		t.Fatalf("boxed tool result should span at least 3 rows, got %d: %q", len(lines), plain)
	}
}

// Truncation must not split a UTF-8 sequence: cutting mid-rune renders a
// replacement character in the box.
func TestToolResultTruncationKeepsUTF8(t *testing.T) {
	m := toolFinishedModel(t, "bash", strings.Repeat("中文内容", 200))

	plain := ansi.Strip(lastBlockText(m))
	for _, r := range plain {
		if r == '\uFFFD' {
			t.Fatalf("tool result contains a broken rune: %q", plain)
		}
	}
}

// Empty output still renders a box, so a silent tool is visibly a tool result
// rather than a blank gap in the transcript.
func TestToolResultEmptyStillBoxed(t *testing.T) {
	m := toolFinishedModel(t, "bash", "")

	plain := ansi.Strip(lastBlockText(m))
	if !strings.Contains(plain, "╭") {
		t.Fatalf("empty tool result is not boxed: %q", plain)
	}
}

// An error result keeps its error marker and is boxed too.
func TestToolResultErrorBoxed(t *testing.T) {
	m := New(nil, "/tmp", "test")
	m.width = 60
	m.height = 30
	m.Update(runtimeEventMsg{event: agent.Event{
		Type:       agent.EventToolCallFinished,
		ToolResult: &agent.ToolResult{Name: "bash", Error: "exit status 1"},
	}})

	plain := ansi.Strip(lastBlockText(m))
	if !strings.Contains(plain, "exit status 1") {
		t.Fatalf("error text lost: %q", plain)
	}
	if !strings.Contains(plain, "╭") {
		t.Fatalf("error result is not boxed: %q", plain)
	}
}

// Long output is capped, but the cap must leave a visible marker rather than
// silently ending mid-word.
func TestToolResultLongOutputIsCapped(t *testing.T) {
	m := toolFinishedModel(t, "bash", strings.Repeat("abcdef", 500))

	plain := ansi.Strip(lastBlockText(m))
	if !strings.Contains(plain, "more lines") {
		t.Fatalf("capped tool result has no truncation marker: %q", plain)
	}
}

// A very narrow terminal must not break the renderer: boxWidth clamps to 1, and
// the content width then clamps below that. The result must still be boxed and
// must not panic.
func TestToolResultNarrowTerminal(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4, 5} {
		m := New(nil, "/tmp", "test")
		m.width = width
		m.height = 10
		m.Update(runtimeEventMsg{event: agent.Event{
			Type:       agent.EventToolCallFinished,
			ToolResult: &agent.ToolResult{Name: "bash", Content: "some output here"},
		}})
		if len(m.blocks) == 0 {
			t.Fatalf("width %d: no block added", width)
		}
		plain := ansi.Strip(lastBlockText(m))
		if plain == "" {
			t.Fatalf("width %d: empty block", width)
		}
	}
}

// Output made of blank lines *and* text must keep its structure and must not be
// reported as empty. Only entirely-whitespace output gets the placeholder.
func TestToolResultBlankLinesPreserved(t *testing.T) {
	m := toolFinishedModel(t, "bash", "first\n\n\nsecond")

	plain := ansi.Strip(lastBlockText(m))
	if strings.Contains(plain, "(no output)") {
		t.Fatalf("output with text was reported as empty: %q", plain)
	}
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("output dropped %q: %q", want, plain)
		}
	}
}

// Entirely whitespace output is the one case that reads as nothing happened.
func TestToolResultWhitespaceOnlyReportedEmpty(t *testing.T) {
	for _, content := range []string{"", " ", "\n", "\n\n", "  \t "} {
		m := toolFinishedModel(t, "bash", content)
		plain := ansi.Strip(lastBlockText(m))
		if !strings.Contains(plain, "(no output)") {
			t.Fatalf("content %q was not reported as empty: %q", content, plain)
		}
	}
}
