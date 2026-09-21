package tui

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	glamour "github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"
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
func renderAssistantMarkdown(text string, width int) string {
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
			part = renderMarkdownDocument(block.text, width)
		}
		part = strings.Trim(part, "\n")
		if part != "" {
			rendered = append(rendered, part)
		}
	}

	return constrainMarkdownWidth(strings.Join(rendered, "\n\n"), width)
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

func splitMarkdownBlocks(source string) []markdownBlock {
	lines := strings.Split(source, "\n")
	blocks := make([]markdownBlock, 0, 3)
	markdownStart := 0

	for i := 0; i < len(lines); i++ {
		fence, language, ok := markdownFenceStart(lines[i])
		if !ok {
			continue
		}

		if i > markdownStart {
			blocks = append(blocks, markdownBlock{text: strings.Join(lines[markdownStart:i], "\n")})
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
		blocks = append(blocks, markdownBlock{text: strings.Join(lines[markdownStart:], "\n")})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, markdownBlock{text: source})
	}
	return blocks
}

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
