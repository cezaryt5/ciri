package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"
)

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "\u2026"
}

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

func fitDotStr(fit predictor.FitStatus) string {
	switch fit {
	case predictor.Recommended:
		return FitDotPerfect.Render("\u25cf")
	case predictor.Advanced:
		return FitDotGood.Render("\u25cf")
	default:
		return "\u25cf"
	}
}

func fitLabel(fit predictor.FitStatus) string {
	switch fit {
	case predictor.Recommended:
		return FitPerfect.Render("Perfect")
	case predictor.Advanced:
		return FitGood.Render("Good")
	default:
		return "Slow"
	}
}

func fitLabelPlain(fit predictor.FitStatus) string {
	switch fit {
	case predictor.Recommended:
		return "Perfect"
	case predictor.Advanced:
		return "Good"
	default:
		return "Slow"
	}
}

func padCell(text string, width int) string {
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().Width(width).Render(text)
}

func renderMiniBox(title, value string, totalWidth int, active bool) string {
	if totalWidth < 10 {
		totalWidth = 10
	}
	innerWidth := totalWidth - 2
	content := " " + title + "  " + value + " "
	if lipgloss.Width(content) > innerWidth {
		content = truncate(content, innerWidth)
	}
	content += repeat(" ", innerWidth-lipgloss.Width(content))
	box := "\u256d" + content + "\u256e"
	if active {
		return SearchActive.Render(box)
	}
	return SearchBar.Render(box)
}

func fitFilterLabel(f fitFilter) string {
	switch f {
	case fitRecommended:
		return "Perfect"
	case fitAdvanced:
		return "Good"
	case fitTooHeavy:
		return "Slow"
	default:
		return "All"
	}
}
