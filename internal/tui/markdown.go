package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	codePreviewHeadLines = 8
	codePreviewTailLines = 2
	codePreviewMaxLines  = codePreviewHeadLines + codePreviewTailLines
)

// renderAssistantMarkdown applies the small Markdown subset used most often in
// agent replies. It intentionally stays lightweight so it can be rerun for
// every streaming delta without constructing a full Markdown renderer.
func renderAssistantMarkdown(text string, width int) string {
	if width <= 0 {
		return text
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			language := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			i++
			var code []string
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				code = append(code, lines[i])
				i++
			}
			if i < len(lines) {
				i++ // consume the closing fence
			}
			out = append(out, renderMarkdownCodeBlock(language, code, width))
			continue
		}

		if level, ok := markdownHeadingLevel(lines[i]); ok {
			wrapped := wordWrap(renderInlineMarkdown(strings.TrimSpace(lines[i])), width)
			out = append(out, markdownHeadingStyle(level).Render(wrapped))
			i++
			continue
		}

		out = append(out, wordWrap(renderInlineMarkdown(lines[i]), width))
		i++
	}
	return strings.Join(out, "\n")
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

func renderInlineMarkdown(line string) string {
	var out strings.Builder
	for len(line) > 0 {
		codeStart := strings.IndexByte(line, '`')
		boldStart := strings.Index(line, "**")

		start := -1
		kind := ""
		switch {
		case codeStart >= 0 && (boldStart < 0 || codeStart < boldStart):
			start, kind = codeStart, "code"
		case boldStart >= 0:
			start, kind = boldStart, "bold"
		default:
			out.WriteString(line)
			return out.String()
		}

		out.WriteString(line[:start])
		if kind == "code" {
			endOffset := strings.IndexByte(line[start+1:], '`')
			if endOffset < 0 {
				out.WriteString(line[start:])
				return out.String()
			}
			end := start + 1 + endOffset
			out.WriteString(inlineCodeStyle.Render(line[start+1 : end]))
			line = line[end+1:]
			continue
		}

		endOffset := strings.Index(line[start+2:], "**")
		if endOffset < 0 {
			// An unmatched marker is markup noise rather than user-facing text.
			// Drop it so terminals without bold support still show clean content.
			out.WriteString(line[start+2:])
			return out.String()
		}
		end := start + 2 + endOffset
		out.WriteString(markdownBoldStyle.Render(line[start+2 : end]))
		line = line[end+2:]
	}
	return out.String()
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
