package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/diffutil"
)

// maxDiffLines bounds how many diff lines a permission prompt renders, so a
// large rewrite cannot push the input bar off screen.
const maxDiffLines = 24

// toolDiffPreview renders what a mutating tool call will change, for the
// permission prompt. It returns "" when the call is not a file modification or
// the arguments cannot be understood.
func toolDiffPreview(call agent.ToolCall, workDir string) string {
	switch call.Name {
	case "edit":
		return editDiffPreview(call.Arguments)
	case "write":
		return writeDiffPreview(call.Arguments, workDir)
	default:
		return ""
	}
}

func editDiffPreview(raw json.RawMessage) string {
	var args struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.OldString == "" {
		return ""
	}
	removed := strings.Split(strings.TrimRight(args.OldString, "\n"), "\n")
	added := strings.Split(strings.TrimRight(args.NewString, "\n"), "\n")
	lines := make([]string, 0, len(removed)+len(added))
	for _, line := range removed {
		lines = append(lines, "- "+line)
	}
	for _, line := range added {
		lines = append(lines, "+ "+line)
	}
	return clipDiff(lines, args.Path)
}

func writeDiffPreview(raw json.RawMessage, workDir string) string {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &args); err != nil || args.Path == "" {
		return ""
	}
	target := args.Path
	if !filepath.IsAbs(target) {
		target = filepath.Join(workDir, target)
	}
	existing, err := os.ReadFile(target)
	if err != nil {
		// A new file: show what will be created.
		lines := make([]string, 0, maxDiffLines)
		for _, line := range strings.Split(strings.TrimRight(args.Content, "\n"), "\n") {
			lines = append(lines, "+ "+line)
		}
		return clipDiff(lines, args.Path+" (new file)")
	}
	return clipDiff(diffutil.LineDiff(string(existing), args.Content), args.Path)
}

// clipDiff caps a diff to maxDiffLines and appends an elision marker.
func clipDiff(lines []string, label string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff %s\n", label)
	shown := lines
	if len(shown) > maxDiffLines {
		shown = shown[:maxDiffLines]
	}
	for _, line := range shown {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(lines) > len(shown) {
		fmt.Fprintf(&b, "… %d more changed lines\n", len(lines)-len(shown))
	}
	return strings.TrimRight(b.String(), "\n")
}
