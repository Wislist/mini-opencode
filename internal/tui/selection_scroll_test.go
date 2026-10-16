package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSelectionStaysOnTranscriptWhenScrolled(t *testing.T) {
	m := selectionTestModel(t)
	m.SetClipboardWriter(func(string) error { return nil })
	m.viewport.GotoTop()
	m.Update(mouseClick(0, 2))
	m.Update(mouseMotion(12, 4))
	m.Update(mouseRelease(12, 4))
	before := m.selectedText()
	m.scrollLines(8)
	if got := m.selectedText(); got != before {
		t.Fatalf("selection changed after scrolling: %q -> %q", before, got)
	}
	_ = m.renderFrame()
	if m.selectionHighlighted {
		t.Fatal("offscreen selection still painted")
	}
	m.viewport.GotoTop()
	_ = m.renderFrame()
	if !m.selectionHighlighted {
		t.Fatal("selection lost when scrolling back")
	}
}

func TestSelectionEdgeScrollContinuesAndStops(t *testing.T) {
	for _, direction := range []int{-1, 1} {
		t.Run(itoa(direction+1), func(t *testing.T) {
			m := selectionTestModel(t)
			m.SetClipboardWriter(func(string) error { return nil })
			m.viewport.SetYOffset(8)
			m.Update(mouseClick(0, 4))
			edge := 1
			if direction > 0 {
				edge = m.viewport.Height()
			}
			_, cmd := m.Update(mouseMotion(20, edge))
			if cmd == nil {
				t.Fatal("edge drag did not schedule scrolling")
			}
			before := m.viewport.YOffset()
			tick := cmd()
			_, next := m.Update(tick)
			if (m.viewport.YOffset()-before)*direction <= 0 {
				t.Fatal("edge tick did not scroll")
			}
			if next == nil {
				t.Fatal("stationary pointer stopped scrolling")
			}
			stale := next()
			m.Update(mouseRelease(20, edge))
			stopped := m.viewport.YOffset()
			m.Update(stale)
			if m.viewport.YOffset() != stopped {
				t.Fatal("scroll continued after release")
			}
		})
	}
}

func TestSelectionEdgeScrollExtendsVisibleHighlightWithoutTearing(t *testing.T) {
	for _, tc := range []struct {
		name      string
		direction int
		startAt   int
		pointerAt func(*Model) int
	}{
		{
			name:      "down",
			direction: 1,
			startAt:   0,
			pointerAt: func(m *Model) int {
				_, bottom := m.selectionViewportBounds()
				return bottom
			},
		},
		{
			name:      "up",
			direction: -1,
			startAt:   12,
			pointerAt: func(m *Model) int {
				top, _ := m.selectionViewportBounds()
				return top
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := selectionTestModel(t)
			m.viewport.SetYOffset(tc.startAt)
			top, bottom := m.selectionViewportBounds()
			anchorRow := top + 1
			if tc.direction < 0 {
				anchorRow = bottom - 1
			}

			m.Update(mouseClick(2, anchorRow))
			_, cmd := m.Update(mouseMotion(20, tc.pointerAt(m)))
			if cmd == nil {
				t.Fatal("edge drag did not schedule scrolling")
			}

			for range 3 {
				msg := cmd()
				_, cmd = m.Update(msg)
				if cmd == nil {
					t.Fatal("stationary edge pointer stopped before reaching the boundary")
				}
			}

			wantEndRow := m.selectionPoint(m.selection.pointer).row
			if m.selection.end.row != wantEndRow {
				t.Fatalf("selection endpoint stayed at row %d after scrolling; want %d", m.selection.end.row, wantEndRow)
			}

			saved := m.selection
			m.selection = selection{}
			plain := m.renderFrame()
			m.selection = saved
			highlighted := m.renderFrame()

			plainLines := strings.Split(plain, "\n")
			highlightedLines := strings.Split(highlighted, "\n")
			if len(highlightedLines) != len(plainLines) {
				t.Fatalf("highlight changed frame height: %d -> %d", len(plainLines), len(highlightedLines))
			}
			if ansi.Strip(highlighted) != ansi.Strip(plain) {
				t.Fatal("highlight changed visible frame text")
			}
			for i := range plainLines {
				if got, want := ansi.StringWidth(highlightedLines[i]), ansi.StringWidth(plainLines[i]); got != want {
					t.Fatalf("row %d width changed: %d -> %d", i, want, got)
				}
			}
			for _, border := range []string{"╭", "╮", "╰", "╯"} {
				if got, want := strings.Count(ansi.Strip(highlighted), border), strings.Count(ansi.Strip(plain), border); got != want {
					t.Fatalf("border %q count changed: %d -> %d", border, want, got)
				}
			}

			highlightedRows := 0
			for row := top; row <= bottom && row < len(highlightedLines); row++ {
				if strings.Contains(highlightedLines[row], ansi.SGR(ansi.AttrReverse)) {
					highlightedRows++
				}
			}
			if highlightedRows < 3 {
				t.Fatalf("long selection highlighted only %d visible row(s)", highlightedRows)
			}
		})
	}
}

func TestSelectionCopiesOffscreenRowsAndUnicode(t *testing.T) {
	m := selectionTestModel(t)
	m.blocks = []string{"中文你好", strings.Repeat("abcdefghij\n", 40)}
	m.markBlocksChanged()
	m.refreshViewport()
	m.viewport.GotoTop()
	_ = m.renderFrame()
	var copied string
	m.SetClipboardWriter(func(s string) error { copied = s; return nil })
	m.Update(mouseClick(0, 1))
	m.Update(mouseMotion(4, 1))
	m.Update(mouseRelease(4, 1))
	if copied != "中文" {
		t.Fatalf("cell-based copy=%q", copied)
	}
	m.Update(mouseClick(0, 1))
	m.scrollLines(20)
	m.Update(mouseMotion(10, 4))
	m.Update(mouseRelease(10, 4))
	if !strings.HasPrefix(copied, "中文你好") || strings.Count(copied, "abcdefghij") < 20 {
		t.Fatalf("offscreen copy missing rows: %q", copied)
	}
}

func TestSelectionAutoScrollBoundaries(t *testing.T) {
	t.Run("pointer leaves edge", func(t *testing.T) {
		m := selectionTestModel(t)
		m.viewport.GotoTop()
		m.Update(mouseClick(0, 3))
		_, cmd := m.Update(mouseMotion(20, m.viewport.Height()))
		if cmd == nil {
			t.Fatal("missing tick")
		}
		tick := cmd()
		m.Update(mouseMotion(20, 4))
		before := m.viewport.YOffset()
		_, next := m.Update(tick)
		if next != nil || m.viewport.YOffset() != before {
			t.Fatal("middle pointer kept scrolling")
		}
	})
	t.Run("old tick cannot move new selection", func(t *testing.T) {
		m := selectionTestModel(t)
		m.viewport.GotoTop()
		m.Update(mouseClick(0, 3))
		_, cmd := m.Update(mouseMotion(20, m.viewport.Height()))
		if cmd == nil {
			t.Fatal("missing tick")
		}
		tick := cmd()
		m.clearSelection()
		m.Update(mouseClick(0, 3))
		before := m.viewport.YOffset()
		m.Update(tick)
		if m.viewport.YOffset() != before {
			t.Fatal("stale tick moved new selection")
		}
	})
	t.Run("bottom stops timer", func(t *testing.T) {
		m := selectionTestModel(t)
		m.viewport.GotoBottom()
		m.Update(mouseClick(0, 3))
		_, cmd := m.Update(mouseMotion(20, m.viewport.Height()+5))
		if cmd != nil {
			t.Fatal("timer scheduled at bottom")
		}
	})
	t.Run("footer selection never scrolls transcript", func(t *testing.T) {
		m := selectionTestModel(t)
		m.viewport.GotoTop()
		m.Update(mouseClick(0, m.height-2))
		_, cmd := m.Update(mouseMotion(20, m.height-1))
		if cmd != nil || m.selection.transcript {
			t.Fatal("footer initiated transcript scroll")
		}
	})
}

func TestSelectionWheelAndReverseCopy(t *testing.T) {
	m := selectionTestModel(t)
	m.viewport.GotoTop()
	var copied string
	m.SetClipboardWriter(func(s string) error { copied = s; return nil })
	m.Update(mouseClick(0, 2))
	m.Update(mouseMotion(12, 4))
	anchor := m.selection.start
	m.Update(wheelDown())
	if m.selection.start != anchor || m.selection.end.row <= 4 {
		t.Fatal("wheel did not extend selection from fixed anchor")
	}
	m.Update(mouseRelease(12, 4))
	before := copied
	m.Update(mouseMotion(1, 1))
	if m.selectedText() != before {
		t.Fatal("motion after release changed selection")
	}
	m.viewport.GotoTop()
	m.Update(mouseClick(12, 4))
	m.Update(mouseMotion(0, 1))
	m.Update(mouseRelease(0, 1))
	reverse := copied
	m.Update(mouseClick(0, 1))
	m.Update(mouseMotion(12, 4))
	m.Update(mouseRelease(12, 4))
	if copied != reverse {
		t.Fatalf("reverse selection differs: %q / %q", reverse, copied)
	}
}
