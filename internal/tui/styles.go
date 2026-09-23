package tui

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
)

// Crush-inspired palette: dark substrate, violet accent, cyan user, green results.
var (
	colorAccent = lipgloss.Color("#7D56F4")
	colorCyan   = lipgloss.Color("#22D3EE")
	colorGreen  = lipgloss.Color("#4ADE80")
	colorRed    = lipgloss.Color("#F87171")
	colorDim    = lipgloss.Color("#6B7280")
	colorFg     = lipgloss.Color("#E4E4E7")
	colorYellow = lipgloss.Color("#FBBF24")
	colorBlue   = lipgloss.Color("#3B82F6")
	colorOrange = lipgloss.Color("#F97316")
)

var (
	userLabel = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	userText  = lipgloss.NewStyle().Foreground(colorFg).PaddingLeft(2)

	assistantLabel = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	assistantText  = lipgloss.NewStyle().Foreground(colorFg).PaddingLeft(2)

	toolBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorDim).Padding(0, 1)
	toolName  = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	toolArrow = lipgloss.NewStyle().Foreground(colorGreen).PaddingLeft(2)
	toolError = lipgloss.NewStyle().Foreground(colorRed).PaddingLeft(2)

	errorStyle   = lipgloss.NewStyle().Foreground(colorRed)
	headerStyle  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	inputBorder  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 1)
	promptStyle  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	spinnerStyle = lipgloss.NewStyle().Foreground(colorAccent)
	permBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorYellow).Padding(0, 1)
	dimStyle     = lipgloss.NewStyle().Foreground(colorDim)
	keyLabel     = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	permAsk      = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)

	cmdStyle = lipgloss.NewStyle().Foreground(colorCyan)

	markdownH1Style   = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	markdownH2Style   = lipgloss.NewStyle().Foreground(colorBlue).Bold(true)
	markdownH3Style   = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	markdownH4Style   = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	markdownBoldStyle = lipgloss.NewStyle().Bold(true)
	inlineCodeStyle   = lipgloss.NewStyle().Foreground(colorCyan).Background(lipgloss.Color("#27272A"))
	markdownCodeBox   = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorGreen).
				Foreground(colorFg).
				Background(lipgloss.Color("#1F2937")).
				Padding(0, 1)
	codeLanguageStyle = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	codeOmittedStyle  = lipgloss.NewStyle().Foreground(colorDim).Italic(true)
)

// Session styles.
var (
	sessionStyle = lipgloss.NewStyle().Foreground(colorDim)
	sessionBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 1)
)

// Command menu styles.
var (
	commandBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorCyan).Padding(0, 1)
)

// Git status styles.
var (
	gitBranchStyle    = lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	gitCleanStyle     = lipgloss.NewStyle().Foreground(colorGreen)
	gitStagedStyle    = lipgloss.NewStyle().Foreground(colorGreen)
	gitModifiedStyle  = lipgloss.NewStyle().Foreground(colorYellow)
	gitUntrackedStyle = lipgloss.NewStyle().Foreground(colorRed)
)

// Diff preview styles used by the permission prompt.
var (
	toolAddedStyle   = lipgloss.NewStyle().Foreground(colorGreen)
	toolRemovedStyle = lipgloss.NewStyle().Foreground(colorRed)
)

// Context usage styles, color-coded by usage tier.
var (
	ctxLowStyle = lipgloss.NewStyle().Foreground(colorGreen)
	// selectionStyle paints a mouse selection. It reverses the colours rather
	// than adding a background so it reads as a selection in any theme.
	selectionStyle = lipgloss.NewStyle().Reverse(true)
	ctxMidStyle    = lipgloss.NewStyle().Foreground(colorYellow)
	ctxHighStyle   = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
)

// promptTextareaStyles returns the textarea styles for the prompt box.
//
// The box supplies its own border, caret and placeholder colours, so the
// textarea's defaults must be cleared of every background: CursorLine and
// EndOfBuffer paint the caret's whole row, and their light defaults render as a
// white bar across the input box. Reverse video is used for the caret instead,
// which stays visible on any terminal theme without introducing a background.
func promptTextareaStyles() textarea.Styles {
	var styles textarea.Styles
	states := []*textarea.StyleState{&styles.Focused, &styles.Blurred}
	for _, state := range states {
		state.Base = lipgloss.NewStyle()
		state.Text = lipgloss.NewStyle().Foreground(colorFg)
		state.CursorLine = lipgloss.NewStyle()
		state.EndOfBuffer = lipgloss.NewStyle()
		state.LineNumber = lipgloss.NewStyle()
		state.CursorLineNumber = lipgloss.NewStyle()
		state.Prompt = lipgloss.NewStyle()
		state.Placeholder = lipgloss.NewStyle().Foreground(colorDim)
	}
	styles.Cursor = textarea.CursorStyle{Blink: true}
	return styles
}
