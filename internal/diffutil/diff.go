// Package diffutil renders small text diffs for previews (permission prompts,
// logs). It is UI-free so both the CLI and the TUI can share it.
package diffutil

import (
	"fmt"
	"strings"
)

// MaxInputLines bounds the inputs a real line diff is computed for. Beyond it
// the diff degrades to a head/tail summary instead of an O(n*m) table.
const MaxInputLines = 600

// LineDiff computes a minimal line diff with a longest-common-subsequence
// table. It falls back to a coarse replacement block for inputs that are too
// large, which keeps the preview cheap for big rewrites.
func LineDiff(oldText, newText string) []string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	if len(oldLines) > MaxInputLines || len(newLines) > MaxInputLines {
		return CoarseDiff(oldLines, newLines)
	}

	// lcs[i][j] = length of the LCS of oldLines[i:] and newLines[j:].
	lcs := make([][]int, len(oldLines)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
				continue
			}
			lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
		}
	}

	var lines []string
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		switch {
		case oldLines[i] == newLines[j]:
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			lines = append(lines, "- "+oldLines[i])
			i++
		default:
			lines = append(lines, "+ "+newLines[j])
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		lines = append(lines, "- "+oldLines[i])
	}
	for ; j < len(newLines); j++ {
		lines = append(lines, "+ "+newLines[j])
	}
	return TrimUnchanged(lines)
}

// TrimUnchanged drops leading/trailing unchanged lines so the preview focuses
// on the actual change, keeping one line of context on each side.
func TrimUnchanged(lines []string) []string {
	isChange := func(line string) bool {
		return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "+ ")
	}
	first, last := -1, -1
	for i, line := range lines {
		if isChange(line) {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 {
		return nil
	}
	start := max(0, first-1)
	end := min(len(lines), last+2)
	return lines[start:end]
}

// CoarseDiff renders a whole-block replacement for oversized inputs.
func CoarseDiff(oldLines, newLines []string) []string {
	const head = 8
	var lines []string
	for i, line := range oldLines {
		if i >= head {
			lines = append(lines, fmt.Sprintf("… %d more old lines", len(oldLines)-head))
			break
		}
		lines = append(lines, "- "+line)
	}
	for i, line := range newLines {
		if i >= head {
			lines = append(lines, fmt.Sprintf("… %d more new lines", len(newLines)-head))
			break
		}
		lines = append(lines, "+ "+line)
	}
	return lines
}

func splitLines(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
