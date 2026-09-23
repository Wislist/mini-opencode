package tui

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	glamour "charm.land/glamour/v2"
	glamouransi "charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	codePreviewHeadLines = 8
	codePreviewTailLines = 2
	codePreviewMaxLines  = codePreviewHeadLines + codePreviewTailLines
)

type markdownBlock struct {
	text     string
	language string
	code     []string
	isCode   bool
}

// renderAssistantMarkdown renders CommonMark plus the GitHub Flavored Markdown
// extensions supported by Glamour. Fenced code blocks remain under our control
// so they can keep the compact bordered preview used by the TUI.
//
// This is the stateless entry point: it builds a throwaway cache, so callers
// that render the same growing text repeatedly (the streaming path) should hold
// a markdownRenderCache instead — see Model.renderAssistantMessage.
func renderAssistantMarkdown(text string, width int) string {
	return renderAssistantMarkdownUncached(text, width)
}

// renderAssistantMarkdownUncached renders every block from scratch. It is the
// reference implementation the cache is validated against, and the fallback for
// one-shot renders.
func renderAssistantMarkdownUncached(text string, width int) string {
	return renderAssistantMarkdownInstrumented(text, width, nil)
}

// renderAssistantMarkdownInstrumented renders the text, calling onDocument for
// every block that actually goes through Glamour. Tests use the callback to
// assert that settled blocks are served from cache instead of being re-rendered.
func renderAssistantMarkdownInstrumented(text string, width int, onDocument func(string)) string {
	if width <= 0 {
		return text
	}

	blocks := splitMarkdownBlocks(strings.ReplaceAll(text, "\r\n", "\n"))
	rendered := make([]string, 0, len(blocks))
	for _, block := range blocks {
		var part string
		if block.isCode {
			part = renderMarkdownCodeBlock(block.language, block.code, width)
		} else {
			if onDocument != nil {
				onDocument(block.text)
			}
			part = renderMarkdownDocument(block.text, width)
		}
		part = strings.Trim(part, "\n")
		if part != "" {
			rendered = append(rendered, part)
		}
	}

	return constrainMarkdownWidth(strings.Join(rendered, "\n\n"), width)
}

// ── Streaming render cache ────────────────────────────
//
// Rendering one assistant message runs Glamour over the whole accumulated
// text, which measures at only ~0.5MB/s (16KB ≈ 30ms on an M2) — well past the
// 16.6ms frame budget once an answer gets long. The throttled repaint in
// stream.go coalesces deltas, but it cannot make a single frame cheaper, so
// long answers still stuttered.
//
// The insight that makes this cheap: during streaming only the *last* block
// can still change. Everything before it is settled — its text is already
// followed by a blank line, or it is a closed code fence — so its rendered
// ANSI form is immutable until the text is replaced. The cache keeps those
// rendered blocks and re-renders only the unsettled tail, turning a per-frame
// O(whole message) Glamour pass into O(trailing block).

// markdownRenderCache memoizes the rendered form of settled markdown blocks for
// one streaming message. It is not safe for concurrent use; the TUI owns it
// from its single update goroutine.
type markdownRenderCache struct {
	width int
	// blocks caches the rendered form of settled blocks, keyed by index, along
	// with sourceText/width so a change of either invalidates the entry.
	blocks []cachedMarkdownBlock
	// source is the full text of the most recent render, used to detect a
	// shrink (undo, retry, interrupt) that invalidates the prefix.
	source string
}

type cachedMarkdownBlock struct {
	sourceText string
	width      int
	rendered   string
}

func newMarkdownRenderCache() *markdownRenderCache {
	return &markdownRenderCache{}
}

// render returns the rendered form of text at width, reusing cached blocks
// wherever the source and width still match.
func (c *markdownRenderCache) render(text string, width int) string {
	return c.renderInstrumented(text, width, nil)
}

// renderInstrumented is render plus a hook reporting each block that had to be
// re-rendered through Glamour. Tests use it to prove settled blocks are cached.
func (c *markdownRenderCache) renderInstrumented(text string, width int, onDocument func(string)) string {
	if width <= 0 {
		return text
	}
	if c == nil {
		return renderAssistantMarkdownInstrumented(text, width, onDocument)
	}

	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	blocks := splitMarkdownBlocks(normalized)

	// A rewrite that is not an extension of the previous text (an undo, a retry,
	// an interrupted stream, a session switch) can invalidate any prefix, so the
	// cache is dropped wholesale rather than diffed.
	if !strings.HasPrefix(normalized, c.source) && c.source != "" {
		c.blocks = nil
	}
	c.source = normalized

	if len(c.blocks) > len(blocks) {
		c.blocks = c.blocks[:len(blocks)]
	}

	rendered := make([]string, 0, len(blocks))
	for i, block := range blocks {
		// A block is reusable only when its source text and width are unchanged
		// and it is settled. Settled means the stream has moved past it: it is a
		// closed code fence, or a later block exists (its separator arrived).
		settled := block.isCode
		if !block.isCode && i < len(blocks)-1 {
			settled = true
		}

		if i < len(c.blocks) {
			entry := c.blocks[i]
			if entry.width == width && entry.sourceText == block.text && entry.sourceTextEqual(block) && settled {
				rendered = appendIfNonEmpty(rendered, entry.rendered)
				continue
			}
		}

		var part string
		if block.isCode {
			part = renderMarkdownCodeBlock(block.language, block.code, width)
		} else {
			if onDocument != nil {
				onDocument(block.text)
			}
			part = renderMarkdownDocument(block.text, width)
		}
		part = strings.Trim(part, "\n")

		// Only settled blocks are worth keeping: an unsettled block changes on
		// the next frame, so caching it would allocate for no benefit.
		if settled {
			entry := cachedMarkdownBlock{sourceText: block.text, width: width, rendered: part}
			if i < len(c.blocks) {
				c.blocks[i] = entry
			} else {
				c.blocks = append(c.blocks, entry)
			}
		}
		rendered = appendIfNonEmpty(rendered, part)
	}

	return constrainMarkdownWidth(strings.Join(rendered, "\n\n"), width)
}

// sourceTextEqual reports whether the cached entry still describes this block.
// Code blocks carry their content in a slice rather than text, so they are
// never treated as reusable by text comparison alone.
func (e cachedMarkdownBlock) sourceTextEqual(block markdownBlock) bool {
	return !block.isCode
}

func appendIfNonEmpty(list []string, part string) []string {
	if part == "" {
		return list
	}
	return append(list, part)
}

func renderMarkdownDocument(source string, width int) string {
	if strings.TrimSpace(source) == "" {
		return ""
	}

	// During streaming, the final strong marker may not have arrived yet.
	// Glamour correctly leaves incomplete Markdown literal, but raw ** is noisy
	// in the TUI, so remove only unmatched strong markers outside code spans.
	source = removeUnmatchedStrongMarkers(source)

	renderer, err := markdownRenderer(width)
	if err != nil {
		return markdownFallback(source, width)
	}

	result, err := renderer.Render(source)
	if err != nil {
		return markdownFallback(source, width)
	}
	return strings.Trim(result, "\n")
}

// markdownRendererCache memoizes one Glamour renderer per terminal width.
// Building a renderer recompiles styles and is comparatively expensive, and
// streaming re-renders the same assistant block on every frame at a stable
// width, so the cache removes that per-frame cost.
var markdownRendererCache = struct {
	mu        sync.Mutex
	renderers map[int]*glamour.TermRenderer
}{renderers: map[int]*glamour.TermRenderer{}}

func markdownRenderer(width int) (*glamour.TermRenderer, error) {
	markdownRendererCache.mu.Lock()
	if r, ok := markdownRendererCache.renderers[width]; ok {
		markdownRendererCache.mu.Unlock()
		return r, nil
	}
	markdownRendererCache.mu.Unlock()

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyleConfig(width)),
		glamour.WithWordWrap(width),
		glamour.WithTableWrap(true),
		glamour.WithEmoji(),
	)
	if err != nil {
		return nil, err
	}

	markdownRendererCache.mu.Lock()
	markdownRendererCache.renderers[width] = r
	markdownRendererCache.mu.Unlock()
	return r, nil
}

func markdownStyleConfig(width int) glamouransi.StyleConfig {
	zero := uint(0)
	one := uint(1)
	blockQuoteToken := "│ "
	tableCenter := "┼"
	tableColumn := "│"
	tableRow := "─"
	bold := true
	italic := true
	crossedOut := true
	underline := true

	return glamouransi.StyleConfig{
		Document: glamouransi.StyleBlock{
			Margin: &zero,
		},
		BlockQuote: glamouransi.StyleBlock{
			StylePrimitive: glamouransi.StylePrimitive{Color: stringPointer("#A1A1AA")},
			Indent:         &one,
			IndentToken:    &blockQuoteToken,
		},
		Paragraph: glamouransi.StyleBlock{},
		List: glamouransi.StyleList{
			LevelIndent: 2,
		},
		Heading: glamouransi.StyleBlock{
			StylePrimitive: glamouransi.StylePrimitive{
				BlockSuffix: "\n",
				Bold:        &bold,
			},
		},
		H1: markdownHeadingBlock(1, "#F87171", &bold),
		H2: markdownHeadingBlock(2, "#3B82F6", &bold),
		H3: markdownHeadingBlock(3, "#7D56F4", &bold),
		H4: markdownHeadingBlock(4, "#FBBF24", &bold),
		H5: markdownHeadingBlock(5, "#22D3EE", &bold),
		H6: markdownHeadingBlock(6, "#A1A1AA", &bold),
		Text: glamouransi.StylePrimitive{
			Color: stringPointer("#E4E4E7"),
		},
		Strong: glamouransi.StylePrimitive{
			Bold: &bold,
		},
		Emph: glamouransi.StylePrimitive{
			Italic: &italic,
		},
		Strikethrough: glamouransi.StylePrimitive{
			CrossedOut: &crossedOut,
		},
		HorizontalRule: glamouransi.StylePrimitive{
			Color:  stringPointer("#6B7280"),
			Format: strings.Repeat("─", max(1, width)),
		},
		Item: glamouransi.StylePrimitive{
			BlockPrefix: "• ",
			Color:       stringPointer("#7D56F4"),
		},
		Enumeration: glamouransi.StylePrimitive{
			BlockPrefix: ". ",
			Color:       stringPointer("#7D56F4"),
		},
		Task: glamouransi.StyleTask{
			StylePrimitive: glamouransi.StylePrimitive{Color: stringPointer("#4ADE80")},
			Ticked:         "[✓] ",
			Unticked:       "[ ] ",
		},
		Link: glamouransi.StylePrimitive{
			Color:     stringPointer("#22D3EE"),
			Underline: &underline,
		},
		LinkText: glamouransi.StylePrimitive{
			Color: stringPointer("#3B82F6"),
			Bold:  &bold,
		},
		Image: glamouransi.StylePrimitive{
			Color:     stringPointer("#22D3EE"),
			Underline: &underline,
		},
		ImageText: glamouransi.StylePrimitive{
			Color:  stringPointer("#A1A1AA"),
			Format: "Image: {{.text}} →",
		},
		Code: glamouransi.StyleBlock{
			StylePrimitive: glamouransi.StylePrimitive{
				Prefix:          "\u00a0",
				Suffix:          "\u00a0",
				Color:           stringPointer("#22D3EE"),
				BackgroundColor: stringPointer("#27272A"),
			},
		},
		Table: glamouransi.StyleTable{
			StyleBlock: glamouransi.StyleBlock{
				StylePrimitive: glamouransi.StylePrimitive{Color: stringPointer("#E4E4E7")},
			},
			CenterSeparator: &tableCenter,
			ColumnSeparator: &tableColumn,
			RowSeparator:    &tableRow,
		},
		DefinitionList: glamouransi.StyleBlock{},
		DefinitionTerm: glamouransi.StylePrimitive{
			Color: stringPointer("#3B82F6"),
			Bold:  &bold,
		},
		DefinitionDescription: glamouransi.StylePrimitive{
			BlockPrefix: "\n  • ",
		},
		HTMLBlock: glamouransi.StyleBlock{
			StylePrimitive: glamouransi.StylePrimitive{Color: stringPointer("#A1A1AA")},
		},
		HTMLSpan: glamouransi.StyleBlock{
			StylePrimitive: glamouransi.StylePrimitive{Color: stringPointer("#A1A1AA")},
		},
	}
}

func markdownHeadingBlock(level int, color string, bold *bool) glamouransi.StyleBlock {
	return glamouransi.StyleBlock{StylePrimitive: glamouransi.StylePrimitive{
		Prefix: strings.Repeat("#", level) + " ",
		Color:  stringPointer(color),
		Bold:   bold,
	}}
}

func stringPointer(value string) *string {
	return &value
}

// splitMarkdownBlocks splits source into renderable blocks at two boundaries:
// fenced code blocks, and blank lines outside those fences.
//
// The blank-line split exists so a streaming message has a settleable prefix.
// Splitting only at fences meant a prose answer was one indivisible block, so
// every throttled repaint re-ran Glamour over the entire accumulated text
// (~0.5MB/s: a 16KB answer costs ~30ms per frame). Splitting at paragraph
// boundaries lets markdownRenderCache keep the finished paragraphs and
// re-render only the trailing one.
//
// The split must not change what is displayed. A blank line is normally a
// block separator, but CommonMark keeps a *loose list* together across blank
// lines: "- a\n- b\n\n- c" is one list, and rendering it as two blocks emitted
// an extra blank line and restarted the bullet run. So a blank line only ends a
// block when the following content does not continue the list that is open at
// that point. markdown_split_test.go pins this against the fence-only splitter
// this replaced.
func splitMarkdownBlocks(source string) []markdownBlock {
	lines := strings.Split(source, "\n")
	blocks := make([]markdownBlock, 0, 3)
	markdownStart := 0

	flushMarkdown := func(end int) {
		for markdownStart < end {
			// Skip the blank separator lines that precede this paragraph.
			start := markdownStart
			for start < end && strings.TrimSpace(lines[start]) == "" {
				start++
			}
			if start >= end {
				break
			}
			// Find the end of this block: a run of blank lines closes it,
			// unless the next non-blank line continues the open list.
			stop := start
			for stop < end {
				if strings.TrimSpace(lines[stop]) != "" {
					stop++
					continue
				}
				// Blank line: look past the run to see what follows.
				next := stop
				for next < end && strings.TrimSpace(lines[next]) == "" {
					next++
				}
				if next < end && continuesOpenList(lines[start:stop], lines[next]) {
					// Still the same loose list; absorb the blank line and go on.
					stop = next
					continue
				}
				break
			}
			blocks = append(blocks, markdownBlock{text: strings.Join(lines[start:stop], "\n")})
			markdownStart = stop
		}
		markdownStart = end
	}

	for i := 0; i < len(lines); i++ {
		fence, language, ok := markdownFenceStart(lines[i])
		if !ok {
			continue
		}

		if i > markdownStart {
			flushMarkdown(i)
		}

		codeStart := i + 1
		i = codeStart
		for i < len(lines) && !markdownFenceClose(lines[i], fence) {
			i++
		}
		blocks = append(blocks, markdownBlock{
			language: language,
			code:     append([]string(nil), lines[codeStart:i]...),
			isCode:   true,
		})

		if i >= len(lines) {
			markdownStart = len(lines)
			break
		}
		markdownStart = i + 1
	}

	if markdownStart < len(lines) {
		flushMarkdown(len(lines))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, markdownBlock{text: source})
	}
	return blocks
}

// continuesOpenList reports whether next should stay in the same block as the
// text that precedes a blank line, so that splitting at blank lines does not
// change how the block renders.
//
// Two cases must be absorbed:
//
//   - A loose list ("- a\n- b\n\n- c") is one list in CommonMark. Splitting it
//     restarted the bullet run and emitted an extra blank line.
//   - A list that directly follows a paragraph or heading ("## Section\n\n- a")
//     renders with different spacing to the blank line our join() inserts
//     between blocks, so the list has to stay attached to the text above it.
//
// Only the immediately preceding non-blank line is considered; continuation by
// indentation is deliberately not attempted, because guessing wrong there would
// change output.
func continuesOpenList(before []string, next string) bool {
	// A list item always stays with what precedes it. This keeps both the loose
	// list and the "paragraph then list" spacing identical to a whole-text
	// render, which is the only property that matters here.
	if b, o := listMarkerKind(next); b || o {
		return true
	}
	last := ""
	for i := len(before) - 1; i >= 0; i-- {
		if strings.TrimSpace(before[i]) != "" {
			last = before[i]
			break
		}
	}
	if last == "" {
		return false
	}
	return sameListMarker(last, next)
}

// sameListMarker reports whether two lines open list items of the same kind:
// two unordered items (any of -, *, +) or two ordered items.
func sameListMarker(a, b string) bool {
	aBullet, aOrdered := listMarkerKind(a)
	if !aBullet && !aOrdered {
		return false
	}
	bBullet, bOrdered := listMarkerKind(b)
	if aBullet {
		return bBullet
	}
	return aOrdered && bOrdered
}

// listMarkerKind classifies a line as an unordered bullet, an ordered item, or
// neither.
func listMarkerKind(line string) (bullet bool, ordered bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" {
		return false, false
	}
	switch trimmed[0] {
	case '-', '*', '+':
		// Require the marker to be followed by a space (or be alone), so a
		// horizontal rule ("---") or a bold run ("**x") is not read as a bullet.
		rest := trimmed[1:]
		if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
			// "---" and "***" are thematic breaks, not list items.
			if strings.Trim(rest, "-*_ \t") == "" && len(rest) >= 2 {
				return false, false
			}
			return true, false
		}
		return false, false
	}
	if trimmed[0] >= '0' && trimmed[0] <= '9' {
		i := 0
		for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
			i++
		}
		if i < len(trimmed) && (trimmed[i] == '.' || trimmed[i] == ')') {
			rest := trimmed[i+1:]
			if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
				return false, true
			}
		}
	}
	return false, false
}

// markdownFenceStart parses a fenced code block opener.
func markdownFenceStart(line string) (string, string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return "", "", false
	}

	marker := trimmed[0]
	count := 0
	for count < len(trimmed) && trimmed[count] == marker {
		count++
	}
	if count < 3 {
		return "", "", false
	}

	info := strings.TrimSpace(trimmed[count:])
	if marker == '`' && strings.ContainsRune(info, '`') {
		return "", "", false
	}
	language := info
	if fields := strings.Fields(info); len(fields) > 0 {
		language = fields[0]
	}
	return strings.Repeat(string(marker), count), language, true
}

func markdownFenceClose(line, openingFence string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || openingFence == "" || trimmed[0] != openingFence[0] {
		return false
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == openingFence[0] {
		count++
	}
	return count >= len(openingFence) && strings.TrimSpace(trimmed[count:]) == ""
}

func removeUnmatchedStrongMarkers(source string) string {
	markers := strongMarkerPositions(source)
	if len(markers)%2 == 0 {
		return source
	}

	removeAt := markers[len(markers)-1]
	return source[:removeAt] + source[removeAt+2:]
}

func strongMarkerPositions(source string) []int {
	positions := make([]int, 0, 4)
	codeFenceLength := 0

	for i := 0; i < len(source); {
		if source[i] == '\\' {
			_, size := utf8.DecodeRuneInString(source[i:])
			i += max(1, size)
			if i < len(source) {
				_, size = utf8.DecodeRuneInString(source[i:])
				i += max(1, size)
			}
			continue
		}
		if source[i] == '`' {
			count := 1
			for i+count < len(source) && source[i+count] == '`' {
				count++
			}
			if codeFenceLength == 0 {
				codeFenceLength = count
			} else if count == codeFenceLength {
				codeFenceLength = 0
			}
			i += count
			continue
		}
		if codeFenceLength == 0 && i+1 < len(source) && source[i:i+2] == "**" {
			positions = append(positions, i)
			i += 2
			continue
		}
		_, size := utf8.DecodeRuneInString(source[i:])
		i += max(1, size)
	}
	return positions
}

func markdownFallback(source string, width int) string {
	return wordWrap(strings.ReplaceAll(source, "**", ""), width)
}

func constrainMarkdownWidth(rendered string, width int) string {
	if width <= 0 || rendered == "" {
		return rendered
	}

	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = trimTrailingMarkdownSpaces(line)
		if ansi.StringWidth(line) <= width {
			out = append(out, line)
			continue
		}
		out = append(out, strings.Split(wordWrap(line, width), "\n")...)
	}
	return strings.Join(out, "\n")
}

func trimTrailingMarkdownSpaces(line string) string {
	plainWidth := ansi.StringWidth(strings.TrimRight(ansi.Strip(line), " \t"))
	if plainWidth == 0 {
		return ""
	}
	if plainWidth == ansi.StringWidth(line) {
		return line
	}
	return ansi.Truncate(line, plainWidth, "")
}

func markdownHeadingLevel(line string) (int, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level < 1 || level > 4 || len(trimmed) <= level || trimmed[level] != ' ' {
		return 0, false
	}
	return level, true
}

func markdownHeadingStyle(level int) lipgloss.Style {
	switch level {
	case 1:
		return markdownH1Style
	case 2:
		return markdownH2Style
	case 3:
		return markdownH3Style
	default:
		return markdownH4Style
	}
}

func renderMarkdownCodeBlock(language string, code []string, width int) string {
	outerWidth := max(1, width)
	styleWidth := max(1, outerWidth-2)   // lipgloss adds the two border cells.
	contentWidth := max(1, styleWidth-2) // horizontal padding inside the box.

	visible, hidden := collapseCodeLines(code)
	body := make([]string, 0, len(visible)+2)
	if language != "" {
		body = append(body, codeLanguageStyle.Render(strings.ToUpper(language)))
	}
	for _, line := range visible {
		if line == codeOmissionMarker(hidden) {
			body = append(body, codeOmittedStyle.Render(line))
			continue
		}
		line = strings.ReplaceAll(line, "\t", "    ")
		body = append(body, ansi.Truncate(line, contentWidth, "…"))
	}
	if len(body) == 0 {
		body = append(body, dimStyle.Render("empty code block"))
	}

	return markdownCodeBox.Width(styleWidth).Render(strings.Join(body, "\n"))
}

func collapseCodeLines(lines []string) ([]string, int) {
	if len(lines) <= codePreviewMaxLines {
		return append([]string(nil), lines...), 0
	}

	hidden := len(lines) - codePreviewMaxLines
	visible := make([]string, 0, codePreviewMaxLines+1)
	visible = append(visible, lines[:codePreviewHeadLines]...)
	visible = append(visible, codeOmissionMarker(hidden))
	visible = append(visible, lines[len(lines)-codePreviewTailLines:]...)
	return visible, hidden
}

func codeOmissionMarker(hidden int) string {
	if hidden <= 0 {
		return ""
	}
	return fmt.Sprintf("⋯ (%d lines hidden)", hidden)
}
