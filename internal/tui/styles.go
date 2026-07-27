package tui

import "github.com/charmbracelet/lipgloss"

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

// Context usage styles, color-coded by usage tier.
var (
	ctxLowStyle  = lipgloss.NewStyle().Foreground(colorGreen)
	ctxMidStyle  = lipgloss.NewStyle().Foreground(colorYellow)
	ctxHighStyle = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
)
