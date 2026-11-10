package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Void Protocol design tokens — see DESIGN(1).md.
//
// The TUI maps the web design system onto a terminal:
//   - void black surface, tonal layers instead of shadows
//   - stark white for primary content, muted gray for mono labels
//   - signal red for status and the crosshair/cursor accents
//   - sharp 1-cell frames with red crosshair (+) corners, no rounded boxes
var (
	viewBG         = lipgloss.Color("#000000") // void black canvas
	deepGray       = lipgloss.Color("#1A1A1A") // inner section rules
	outlineVariant = lipgloss.Color("#444748") // inactive marks
	mutedGray      = lipgloss.Color("#888888") // mono labels / metadata
	starkWhite     = lipgloss.Color("#FFFFFF") // primary content + frame edges
	onSurface      = lipgloss.Color("#E2E2E2") // body text
	onSurfaceVar   = lipgloss.Color("#C4C7C8") // secondary values
	signalRed      = lipgloss.Color("#FF4D4D") // alerts / slow fits
)

var (
	// Typography. Inter roles become weight/case in the terminal; the
	// JetBrains Mono roles are the uppercase labels and tabular values.
	Wordmark  = lipgloss.NewStyle().Foreground(starkWhite).Bold(true)
	Tagline   = lipgloss.NewStyle().Foreground(mutedGray)
	Heading   = lipgloss.NewStyle().Foreground(starkWhite).Bold(true)
	Label     = lipgloss.NewStyle().Foreground(mutedGray)
	Value     = lipgloss.NewStyle().Foreground(onSurface)
	ValueDim  = lipgloss.NewStyle().Foreground(onSurfaceVar)
	Faint     = lipgloss.NewStyle().Foreground(outlineVariant)
	HintKey   = lipgloss.NewStyle().Foreground(starkWhite)
	HintLabel = lipgloss.NewStyle().Foreground(mutedGray)

	// Structure. Frame edges are white; inner section rules stay quiet.
	Rule      = lipgloss.NewStyle().Foreground(deepGray)
	FrameRule = lipgloss.NewStyle().Foreground(starkWhite)
	Border    = lipgloss.NewStyle().Foreground(starkWhite)
	Corner    = lipgloss.NewStyle().Foreground(signalRed)

	// Cursor is the selection marker used by every list. Signal red keeps it
	// distinct from the selected row's white text.
	Cursor = lipgloss.NewStyle().Foreground(signalRed).Background(viewBG)

	// Status (8x8 squares, never circles).
	StatusOn    = lipgloss.NewStyle().Foreground(starkWhite)
	StatusOff   = lipgloss.NewStyle().Foreground(outlineVariant)
	StatusAlert = lipgloss.NewStyle().Foreground(signalRed)

	// Interaction. Selection stays on the black canvas; the row marker is
	// the selection affordance rather than a lighter fill. Explicit white
	// keeps plain cells readable regardless of the terminal theme.
	SelectedRow = lipgloss.NewStyle().Foreground(starkWhite).Background(viewBG)

	// Fit assessment.
	FitPerfect = lipgloss.NewStyle().Foreground(starkWhite)
	FitGood    = lipgloss.NewStyle().Foreground(mutedGray)
	FitSlow    = lipgloss.NewStyle().Foreground(signalRed)

	// Memory pressure. Quiet when safe, white when tight, red when critical.
	MemPctGood = lipgloss.NewStyle().Foreground(mutedGray)
	MemPctWarn = lipgloss.NewStyle().Foreground(onSurface)
	MemPctBad  = lipgloss.NewStyle().Foreground(signalRed)
)

// RenderPanel draws a Level 1 container: a sharp 1-cell white frame with
// crosshair (+) accents at all four corners. Title is a mono label.
func RenderPanel(title, content string, width int) string {
	if width < 10 {
		width = 10
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	return renderFrame(Label.Render(strings.ToUpper(title)), "", lines, width)
}

// renderFrame is the single frame renderer used by both panels and the
// hardware/brand chrome. left and right are pre-styled strings.
func renderFrame(left, right string, content []string, width int) string {
	if width < 10 {
		width = 10
	}
	inner := width - 2

	var b strings.Builder
	b.WriteString(frameTop(left, right, width))
	b.WriteString("\n")
	for _, line := range content {
		b.WriteString(Border.Render("│"))
		b.WriteString(padLine(line, inner))
		b.WriteString(Border.Render("│"))
		b.WriteString("\n")
	}
	b.WriteString(Corner.Render("+"))
	b.WriteString(FrameRule.Render(strings.Repeat("─", inner)))
	b.WriteString(Corner.Render("+"))
	return b.String()
}

func frameTop(left, right string, width int) string {
	if width < 10 {
		width = 10
	}
	left = strings.TrimRight(left, " ")
	right = strings.TrimRight(right, " ")

	if right != "" {
		maxRight := width - 10
		if maxRight < 3 {
			right = ""
		} else if lipgloss.Width(right) > maxRight {
			right = ansi.Truncate(right, maxRight, "…")
		}
	}

	maxLeft := width - 5
	if right != "" {
		maxLeft = width - lipgloss.Width(right) - 7
	}
	if maxLeft < 1 {
		maxLeft = 1
	}
	if lipgloss.Width(left) > maxLeft {
		left = ansi.Truncate(left, maxLeft, "…")
	}

	if right == "" {
		dashes := width - lipgloss.Width(left) - 4
		if dashes < 1 {
			dashes = 1
		}
		return Corner.Render("+") + " " + left + " " +
			FrameRule.Render(strings.Repeat("─", dashes)) + Corner.Render("+")
	}

	dashes := width - lipgloss.Width(left) - lipgloss.Width(right) - 6
	if dashes < 1 {
		dashes = 1
	}
	return Corner.Render("+") + " " + left + " " +
		FrameRule.Render(strings.Repeat("─", dashes)) + " " + right + " " + Corner.Render("+")
}

// padLine fits a rendered line into exactly width cells. Truncation is
// ANSI-aware so styled cells never spill past the frame.
func padLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = ansi.Truncate(line, width, "")
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(line)
}

// RenderRule returns a horizontal Deep Gray line, used under table headers
// and between sections. Data tables use no vertical lines.
func RenderRule(width int) string {
	if width < 1 {
		return ""
	}
	return Rule.Render(strings.Repeat("─", width))
}

// RenderSection labels a section inline on a Deep Gray rule, e.g.
//
//	SPECIFICATION ─────────────────────────────────────────
func RenderSection(title string, width int) string {
	prefix := "  " + Label.Render(strings.ToUpper(title)) + " "
	if width <= lipgloss.Width(prefix) {
		return prefix
	}
	return prefix + Rule.Render(strings.Repeat("─", width-lipgloss.Width(prefix)))
}

// renderChip renders a small rectangular tag with mono-label typography.
func renderChip(key, label, value string, active bool) string {
	edge := Faint
	if active {
		edge = StatusOn
	}
	content := HintKey.Render(key) + " " + Label.Render(strings.ToUpper(label)) + " " + Value.Render(value)
	if active {
		content = HintKey.Render(key) + " " + Label.Render(strings.ToUpper(label)) + " " + StatusOn.Render(value)
	}
	return edge.Render("[") + content + edge.Render("]")
}

// keyHint is a single "KEY action" pair in the footer.
type keyHint struct {
	key   string
	label string
}

// renderHints fits as many key hints as possible on one line. Hints that
// do not fit are dropped, so the footer never wraps or overflows.
func renderHints(width int, hints ...keyHint) string {
	if width <= 0 {
		width = 80
	}
	const gap = "   "
	var b strings.Builder
	used := 0
	for _, h := range hints {
		seg := HintKey.Render(strings.ToUpper(h.key)) + " " + HintLabel.Render(strings.ToUpper(h.label))
		segW := lipgloss.Width(seg)
		need := segW
		if used > 0 {
			need += len(gap)
		}
		if used+need > width {
			break
		}
		if used > 0 {
			b.WriteString(gap)
		}
		b.WriteString(seg)
		used += need
	}
	return b.String()
}

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(s, n)
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	return ansi.Truncate(s, max, "\u2026")
}

// paintCanvas pads every rendered line to the full terminal width and wraps
// it in the void-black background. The padding happens inside the styled
// render, so the background covers the entire line rather than only its
// glyphs — the TUI owns the whole terminal instead of inheriting the user's
// scheme. Styled spans inside (alerts) keep their own colors on top.
func paintCanvas(view string, width int) string {
	return paintCanvasHeight(view, width, 0)
}

// paintCanvasHeight is paintCanvas plus a height pad. Padding to the terminal
// height keeps every row black, including the empty rows below short screens.
func paintCanvasHeight(view string, width, height int) string {
	if width <= 0 {
		return view
	}
	st := lipgloss.NewStyle().Background(viewBG).Width(width).MaxWidth(width)
	lines := strings.Split(view, "\n")
	if height > len(lines) {
		lines = append(lines, make([]string, height-len(lines))...)
	}
	for i, line := range lines {
		line = ansi.Truncate(line, width, "")
		lines[i] = st.Render(line)
	}
	return strings.Join(lines, "\n")
}
