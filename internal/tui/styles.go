package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	base = lipgloss.NewStyle()

	// Use terminal ANSI palette colors so the app matches the user's
	// terminal theme (like llmfit) instead of forcing fixed hex values.
	green  = lipgloss.Color("2")
	yellow = lipgloss.Color("3")
	red    = lipgloss.Color("1")
	cyan   = lipgloss.Color("6")

	// Hardware bar
	Bar        = lipgloss.NewStyle()
	BarAccent  = lipgloss.NewStyle()
	BarSuccess = lipgloss.NewStyle()

	// Section headers
	SectionRecommended = lipgloss.NewStyle().Bold(true)
	SectionAdvanced    = lipgloss.NewStyle().Bold(true)

	// Model list items
	ModelName      = lipgloss.NewStyle()
	ModelParams    = lipgloss.NewStyle()
	ModelQuant     = lipgloss.NewStyle()
	ModelSpeed     = lipgloss.NewStyle()
	ModelSpeedSlow = lipgloss.NewStyle()
	ModelCheck     = lipgloss.NewStyle()
	ModelWarn      = lipgloss.NewStyle()

	// Detail screen
	DetailLabel   = lipgloss.NewStyle().Width(16)
	DetailValue   = lipgloss.NewStyle()
	DetailPerfect = lipgloss.NewStyle().Foreground(green).Bold(true)
	DetailSlow    = lipgloss.NewStyle().Foreground(yellow).Bold(true)

	// Footer
	Footer = lipgloss.NewStyle()

	// Selection cursor
	Cursor = lipgloss.NewStyle()

	// Category menu item
	CategoryName  = lipgloss.NewStyle()
	CategoryCount = lipgloss.NewStyle()

	// Divider
	Divider = lipgloss.NewStyle()

	// Title
	Title = lipgloss.NewStyle().Bold(true)

	// Table
	TableHeader = lipgloss.NewStyle().Bold(true)

	FitDotPerfect = lipgloss.NewStyle().Foreground(green)
	FitDotGood    = lipgloss.NewStyle().Foreground(yellow)
	// Reverse video uses the terminal's own fg/bg, giving a clean highlight
	// that always matches the active theme (the look llmfit has).
	SelectedRow = lipgloss.NewStyle().Reverse(true)
	SelectedDot = lipgloss.NewStyle().Foreground(cyan)
	PreviewBar  = lipgloss.NewStyle()

	FitPerfect = lipgloss.NewStyle().Foreground(green)
	FitGood    = lipgloss.NewStyle().Foreground(yellow)

	// Search / filter bar
	SearchBar    = lipgloss.NewStyle().Foreground(cyan)
	SearchActive = lipgloss.NewStyle().Bold(true).Foreground(cyan)

	// Memory percentage
	MemPctGood = lipgloss.NewStyle().Foreground(green)
	MemPctWarn = lipgloss.NewStyle().Foreground(yellow)
	MemPctBad  = lipgloss.NewStyle().Foreground(red)
)

// RenderLabeledLine — internal/tui/styles.go:83
// Called from: (defined, currently uncalled)
// Renders a section divider with a label in the middle (e.g. "── label ──").
// Pads with "─" to the specified width.
func RenderLabeledLine(label string, width int) string {
	if width <= 0 {
		width = 80
	}
	prefix := "── " + label + " "
	pad := width - len(prefix)
	if pad < 0 {
		pad = 0
	}
	return prefix + repeat("─", pad)
}

// RenderDivider — internal/tui/styles.go:95
// Called from: results.go:228 (in resultsView); benchmarks.go:98 (in benchView)
// Renders a horizontal divider line composed of "─" characters to the
// specified width.
func RenderDivider(width int) string {
	if width <= 0 {
		width = 80
	}
	return repeat("─", width)
}

// RenderBox — internal/tui/styles.go:102
// Called from: app.go:117,124,130,135,141 (in App.View)
// Renders a bordered box with a top title bar (e.g. "── title ──┐") around
// the given content. Each content line is padded to innerWidth. Bottom
// border closes the box. Minimum width is 4.
func RenderBox(title string, content string, width int) string {
	if width < 4 {
		width = 4
	}
	innerWidth := width - 2

	topTitle := "── " + title + " "
	topLen := lipgloss.Width(topTitle)
	topPad := width - topLen - 2
	if topPad < 0 {
		topTitle = "── " + truncate(title, width-8) + " "
		topLen = lipgloss.Width(topTitle)
		topPad = width - topLen - 2
		if topPad < 0 {
			topPad = 0
		}
	}
	topBorder := "┌" + topTitle + repeat("─", topPad) + "┐"

	var b strings.Builder
	b.WriteString(topBorder + "\n")

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		padded := lipgloss.NewStyle().Width(innerWidth).Render(line)
		b.WriteString("│" + padded + "│\n")
	}

	btmBorder := "└" + repeat("─", innerWidth) + "┘"
	b.WriteString(btmBorder)

	return b.String()
}

// repeat — internal/tui/styles.go:136
// Called from: styles.go:89,92,99,119,130 (in RenderLabeledLine, RenderDivider, RenderBox); results.go:290 (in searchBar)
// Returns a string consisting of s repeated n times. Used for generating
// border lines and padding.
func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
