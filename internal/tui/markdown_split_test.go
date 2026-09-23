package tui

import (
	"strings"
	"testing"
)

// fenceOnlySplit reproduces the pre-change splitMarkdownBlocks (fence-only
// splitting), so the blank-line splitter can be checked against the output the
// TUI produced before it existed.
func fenceOnlySplit(source string) []markdownBlock {
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

// fenceOnlyRender is the pre-change render path built on fenceOnlySplit.
func fenceOnlyRender(text string, width int) string {
	if width <= 0 {
		return text
	}
	blocks := fenceOnlySplit(strings.ReplaceAll(text, "\r\n", "\n"))
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

// TestSplitRenderMatchesFenceOnlyRenderer is the correctness gate for the
// blank-line split: the new splitter must render byte-for-byte identically to
// the fence-only splitter it replaced, across widths and across the markdown
// constructs whose meaning depends on blank lines (loose lists, the spacing of
// a list that follows a paragraph, tables, quotes, fences).
func TestSplitRenderMatchesFenceOnlyRenderer(t *testing.T) {
	samples := []string{
		"para one\n\npara two\n\npara three",
		"# Title\n\nintro text\n\n## Section\n\nbody text\n\n- a\n- b\n\n- c",
		"text with **bold**\n\nmore *italic* text\n\nlast",
		"> quote block\n\nafter quote",
		"1. one\n2. two\n\n3. three",
		"```go\ncode here\n```\n\npara\n\n```sh\nmore code\n```",
		"a\nb\nc\n\nd\ne\nf",
		"| a | b |\n|---|---|\n| 1 | 2 |\n\npara after table",
		"para\n\n\n\ntriple blank\n\n\n\ntail",
		"trailing spaces   \n\nnext",
		"**unclosed bold\n\nnext para",
		"# H1\n\n- one\n- two\n\ntext\n\n```py\nprint(1)\n```\n\nend",
		"single paragraph with no breaks at all",
		"",
	}
	for _, w := range []int{40, 80, 120} {
		for i, s := range samples {
			want := fenceOnlyRender(s, w)
			got := renderAssistantMarkdownUncached(s, w)
			if got != want {
				t.Errorf("width=%d sample=%d DIFFERS\n src: %q\n new: %q\n old: %q", w, i, s, got, want)
			}
		}
	}
}
