package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func pressInputKeyForOS(m *Model, name, goos string) *Model {
	model, _ := m.handleIdleKeyForOS(key(name), goos)
	return model.(*Model)
}

func TestInputSelectAllUsesPlatformShortcut(t *testing.T) {
	tests := []struct {
		name string
		goos string
		key  string
		want bool
	}{
		{name: "macOS command A", goos: "darwin", key: "super+a", want: true},
		{name: "macOS control A remains line start", goos: "darwin", key: "ctrl+a", want: false},
		{name: "Windows control A", goos: "windows", key: "ctrl+a", want: true},
		{name: "Windows super A is not select all", goos: "windows", key: "super+a", want: false},
		{name: "Linux keeps textarea control A", goos: "linux", key: "ctrl+a", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInputSelectAllKey(tc.key, tc.goos); got != tc.want {
				t.Fatalf("isInputSelectAllKey(%q, %q) = %v, want %v", tc.key, tc.goos, got, tc.want)
			}
		})
	}
}

func TestInputSelectAllTypingReplacesWholeQuestion(t *testing.T) {
	for _, tc := range []struct {
		goos string
		key  string
	}{
		{goos: "darwin", key: "super+a"},
		{goos: "windows", key: "ctrl+a"},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			m := inputTestModel(t)
			m.input.SetValue("first line\n第二行")
			m = pressInputKeyForOS(m, tc.key, tc.goos)
			m = pressInputKeyForOS(m, "x", tc.goos)

			if got := m.input.Value(); got != "x" {
				t.Fatalf("typing after select all = %q, want %q", got, "x")
			}
			if m.inputSelectAll {
				t.Fatal("selection stayed active after replacement")
			}
		})
	}
}

func TestInputSelectAllPasteReplacesWholeQuestion(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("old\nquestion")
	m = pressInputKeyForOS(m, "super+a", "darwin")

	model, _ := m.Update(tea.PasteMsg{Content: "new\n内容"})
	m = model.(*Model)

	if got := m.input.Value(); got != "new\n内容" {
		t.Fatalf("paste after select all = %q, want %q", got, "new\n内容")
	}
	if m.inputSelectAll {
		t.Fatal("selection stayed active after paste")
	}
}

func TestInputSelectAllDeleteClearsWholeQuestion(t *testing.T) {
	for _, name := range []string{"backspace", "delete"} {
		t.Run(name, func(t *testing.T) {
			m := inputTestModel(t)
			m.input.SetValue("first line\n第二行")
			m = pressInputKeyForOS(m, "super+a", "darwin")
			m = pressInputKeyForOS(m, name, "darwin")

			if got := m.input.Value(); got != "" {
				t.Fatalf("%s after select all left %q", name, got)
			}
			if m.inputSelectAll {
				t.Fatal("selection stayed active after delete")
			}
		})
	}
}

func TestInputSelectAllEscapeCancelsWithoutDeleting(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("keep\nthis")
	m = pressInputKeyForOS(m, "super+a", "darwin")
	m = pressInputKeyForOS(m, "esc", "darwin")

	if got := m.input.Value(); got != "keep\nthis" {
		t.Fatalf("escape changed input to %q", got)
	}
	if m.inputSelectAll {
		t.Fatal("escape did not cancel input selection")
	}
}

func TestInputSelectAllNewlineReplacesWholeQuestion(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("replace\nall")
	m = pressInputKeyForOS(m, "super+a", "darwin")
	m = pressInputKeyForOS(m, "ctrl+j", "darwin")

	if got := m.input.Value(); got != "\n" {
		t.Fatalf("newline after select all = %q, want one newline", got)
	}
	if m.inputSelectAll {
		t.Fatal("selection stayed active after newline replacement")
	}
}

func TestInputSelectAllNavigationCancelsWithoutChangingQuestion(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("keep this")
	m.input.CursorEnd()
	m = pressInputKeyForOS(m, "super+a", "darwin")
	m = pressInputKeyForOS(m, "left", "darwin")

	if got := m.input.Value(); got != "keep this" {
		t.Fatalf("navigation changed input to %q", got)
	}
	if m.inputSelectAll {
		t.Fatal("navigation did not cancel input selection")
	}
}

func TestInputSelectAllSendClearsSelectionState(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("send this")
	m.SetRuntime(nil)
	m = pressInputKeyForOS(m, "super+a", "darwin")
	m = pressInputKeyForOS(m, "enter", "darwin")

	if m.inputSelectAll {
		t.Fatal("send left input selection active")
	}
}

func TestInputSelectAllIsIdempotentAndSafeForEmptyInput(t *testing.T) {
	m := inputTestModel(t)
	m = pressInputKeyForOS(m, "super+a", "darwin")
	if m.inputSelectAll {
		t.Fatal("empty input should not enter selected state")
	}

	m.input.SetValue("question")
	m = pressInputKeyForOS(m, "super+a", "darwin")
	m = pressInputKeyForOS(m, "super+a", "darwin")
	if !m.inputSelectAll {
		t.Fatal("repeating select all cleared the selection")
	}
}

func TestInputSelectAllHighlightPreservesMultilineFrameGeometry(t *testing.T) {
	m := inputTestModel(t)
	m.input.SetValue("first line\n中文第二行\nthird line")
	m.syncInputHeight()

	plain := m.renderInputBar()
	m = pressInputKeyForOS(m, "super+a", "darwin")
	selected := m.renderInputBar()

	if !m.inputSelectAllHighlighted {
		t.Fatal("selected input was not highlighted")
	}
	if got, want := ansi.Strip(selected), ansi.Strip(plain); got != want {
		t.Fatalf("highlight changed visible text:\n got %q\nwant %q", got, want)
	}
	plainLines := strings.Split(plain, "\n")
	selectedLines := strings.Split(selected, "\n")
	if len(selectedLines) != len(plainLines) {
		t.Fatalf("highlight changed line count: %d -> %d", len(plainLines), len(selectedLines))
	}
	for i := range plainLines {
		if got, want := ansi.StringWidth(selectedLines[i]), ansi.StringWidth(plainLines[i]); got != want {
			t.Fatalf("line %d width changed: %d -> %d", i, want, got)
		}
	}
	for _, corner := range []string{"╭", "╮", "╰", "╯"} {
		if got, want := strings.Count(selected, corner), strings.Count(plain, corner); got != want {
			t.Fatalf("corner %q count changed: %d -> %d", corner, want, got)
		}
	}
}

func TestInputSelectAllNeverTearsShortTerminalFrame(t *testing.T) {
	for _, height := range []int{12, 8, 7, 6, 5, 4, 3} {
		m := inputTestModel(t)
		model, _ := m.Update(tea.WindowSizeMsg{Width: 42, Height: height})
		m = model.(*Model)
		m.input.SetValue(strings.Repeat("selected 中文 line\n", 10) + "last")
		m.syncInputHeight()
		m = pressInputKeyForOS(m, "super+a", "darwin")

		frame := m.renderFrame()
		if got := lineCount(frame); got > height {
			t.Fatalf("height=%d: selected frame has %d rows", height, got)
		}
		for _, pair := range [][2]string{{"╭", "╰"}, {"╮", "╯"}} {
			if opens, closes := strings.Count(frame, pair[0]), strings.Count(frame, pair[1]); opens != closes {
				t.Fatalf("height=%d: corners %q/%q are unbalanced: %d/%d", height, pair[0], pair[1], opens, closes)
			}
		}
	}
}
