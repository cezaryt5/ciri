package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"
)

func formatDate(date string) string {
	if date == "" || len(date) < 7 {
		return "\u2014"
	}
	return date[:7]
}

func formatMode(fit predictor.FitStatus) string {
	if fit == predictor.Recommended {
		return "GPU"
	}
	return "CPU"
}

func formatMemPctRaw(m *model.Model, gpu *hardware.GPU) string {
	if gpu == nil || gpu.VRAMGB <= 0 {
		return "\u2014"
	}
	needed := predictor.ModelVRAMRequirement(m)
	if needed <= 0 {
		return "\u2014"
	}
	pct := needed / gpu.VRAMGB * 100
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("%.0f%%", pct)
}

// statusSquare renders the design system's 8x8 status indicator: a square,
// never a circle. Signal red is reserved for alerts.
func statusSquare(on bool) string {
	if on {
		return StatusOn.Render("\u25a0")
	}
	return StatusOff.Render("\u25a0")
}

func fitSquare(fit predictor.FitStatus) string {
	switch fit {
	case predictor.Recommended:
		return FitPerfect.Render("\u25a0")
	case predictor.Advanced:
		return FitGood.Render("\u25a0")
	default:
		return FitSlow.Render("\u25a0")
	}
}

func fitLabel(fit predictor.FitStatus) string {
	switch fit {
	case predictor.Recommended:
		return FitPerfect.Render("PERFECT")
	case predictor.Advanced:
		return FitGood.Render("GOOD")
	default:
		return FitSlow.Render("SLOW")
	}
}

func fitLabelPlain(fit predictor.FitStatus) string {
	switch fit {
	case predictor.Recommended:
		return "PERFECT"
	case predictor.Advanced:
		return "GOOD"
	default:
		return "SLOW"
	}
}

func fitStatusTag(fit predictor.FitStatus) string {
	return fitSquare(fit) + " " + fitLabel(fit)
}

// field renders a mono label followed by a value, padded to a fixed label
// width so consecutive fields align like a data table.
func field(label, value string, labelW int) string {
	if strings.TrimSpace(value) == "" {
		value = "\u2014"
	}
	l := lipgloss.NewStyle().Width(labelW).Render(Label.Render(strings.ToUpper(label)))
	return l + Value.Render(value)
}

// renderCell pads text into width cells and appends a single gutter space.
// The gutter is part of the column width, so header and rows share geometry.
func renderCell(text string, width int, align lipgloss.Position) string {
	if width <= 0 {
		return ""
	}
	contentW := width - 1
	if contentW < 1 {
		contentW = 1
	}
	text = ansi.Truncate(text, contentW, "")
	st := lipgloss.NewStyle().Width(contentW).MaxWidth(contentW)
	if align != lipgloss.Left {
		st = st.Align(align)
	}
	return st.Render(text) + " "
}

func centerLine(text string, width int) string {
	if width <= 0 {
		return text
	}
	tw := lipgloss.Width(text)
	if tw >= width {
		return truncate(text, width)
	}
	return strings.Repeat(" ", (width-tw)/2) + text
}

// padRight pads a rendered string to width, truncating when it overflows.
func padRight(s string, width int) string {
	if width <= 0 {
		return s
	}
	s = ansi.Truncate(s, width, "")
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(s)
}

// alignLine places left and right on one line, right-aligned. The right
// side wins when space is tight.
func alignLine(left, right string, width int) string {
	if width <= 0 {
		return left
	}
	rw := lipgloss.Width(right)
	if rw == 0 {
		return padRight(left, width)
	}
	if rw+2 >= width {
		return truncate(right, width)
	}
	leftW := width - rw
	left = padRight(truncate(left, leftW-2), leftW)
	return left + right
}

func fitFilterLabel(f fitFilter) string {
	switch f {
	case fitRecommended:
		return "PERFECT"
	case fitAdvanced:
		return "GOOD"
	case fitTooHeavy:
		return "SLOW"
	default:
		return "ALL"
	}
}

// versionLabel formats the build version for the chrome and status strip.
func versionLabel(v string) string {
	if v == "" {
		return ""
	}
	if v == "dev" || strings.HasPrefix(v, "dev-") {
		return "DEV BUILD"
	}
	return "v" + strings.TrimPrefix(v, "v")
}

// formatBytes renders a byte count in human-friendly units.
func formatBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 5 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
