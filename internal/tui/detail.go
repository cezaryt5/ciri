package tui

import (
	"fmt"
	"strings"

	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
	"github.com/cezaryt5/Can_I_Run_IT/internal/model"
	"github.com/cezaryt5/Can_I_Run_IT/internal/predictor"

	tea "github.com/charmbracelet/bubbletea"
)

type detailModel struct {
	selected predictor.ModelPrediction
	category model.Category
	specs    hardware.Specs
	gpu      *hardware.GPU
}

// newDetailModel — internal/tui/detail.go:21
// Called from: app.go:133 (in App.View, when screenDetail is shown without an existing detail)
// Creates a detailModel. This is a minimal constructor; the actual selected
// model is set in results.go when navigating to detail.
func newDetailModel(pred *predictor.Predictor, cat model.Category, specs hardware.Specs, gpu *hardware.GPU) *detailModel {
	return &detailModel{
		category: cat,
		specs:    specs,
		gpu:      gpu,
	}
}

// detailUpdate — internal/tui/detail.go:29
// Called from: app.go:103 (in App.Update)
// Handles keyboard input on the detail screen: Esc returns to results,
// B opens benchmarks.
func (d *detailModel) detailUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return func() tea.Msg { return navigateMsg{target: screenResults} }
	case "b":
		return func() tea.Msg { return navigateMsg{target: screenBenchmarks} }
	}
	return nil
}

// detailView — internal/tui/detail.go:39
// Called from: app.go:135 (in App.View)
// Renders the full detail view for a model: name, provider, params, quant,
// format, context, architecture, pipeline, resource requirements (RAM/VRAM),
// fit assessment with colored status, estimated speed, VRAM usage
// percentage, and community stats (downloads, likes).
func (d *detailModel) detailView(a *App) string {
	m := d.selected.Model
	var b strings.Builder

	name := truncate(m.Name, 50)
	b.WriteString(Title.Render("  " + name) + "\n\n")

	// Provider / params / quant / format
	b.WriteString(detailRow("Provider", m.Provider) + "\n")
	b.WriteString(detailRow("Parameters", m.ParameterCount) + "\n")
	b.WriteString(detailRow("Quantization", m.Quantization) + "\n")
	b.WriteString(detailRow("Format", m.Format) + "\n")
	if m.ContextLength > 0 {
		b.WriteString(detailRow("Context Length", formatContext(m.ContextLength)) + "\n")
	}
	b.WriteString(detailRow("Architecture", m.Architecture) + "\n")
	b.WriteString(detailRow("Pipeline", m.PipelineTag) + "\n")

	b.WriteString("\n  Resources\n")
	b.WriteString(detailRow("  Min RAM", fmt.Sprintf("%.1f GB", m.MinRAMGB)) + "\n")
	b.WriteString(detailRow("  Recommended", fmt.Sprintf("%.1f GB", m.RecommendedRAMGB)) + "\n")
	b.WriteString(detailRow("  Min VRAM", fmt.Sprintf("%.1f GB", m.MinVRAMGB)) + "\n")

	b.WriteString("\n  Fit Assessment\n")
	if d.selected.FitStatus == predictor.Recommended {
		b.WriteString(DetailPerfect.Render(detailRow("  Status", "Perfect (fits in VRAM)")) + "\n")
	} else {
		b.WriteString(DetailSlow.Render(detailRow("  Status", "Spills to RAM - will be slow")) + "\n")
	}
	b.WriteString(detailRow("  Est. Speed", fmt.Sprintf("%.0f tok/s", d.selected.EstTokPerSec)) + "\n")

	if d.gpu != nil && d.gpu.VRAMGB > 0 && m.MinVRAMGB > 0 {
		pct := m.MinVRAMGB / d.gpu.VRAMGB * 100
		b.WriteString(detailRow("  VRAM Usage", fmt.Sprintf("%.0f%%", pct)) + "\n")
	}

	b.WriteString("\n  Community\n")
	b.WriteString(detailRow("  Downloads", formatNum(m.HfDownloads)) + "\n")
	b.WriteString(detailRow("  Likes", formatNum(m.HfLikes)) + "\n")

	b.WriteString("\n" + Footer.Render("  Esc Back  B Benchmarks"))
	return b.String()
}

// detailRow — internal/tui/detail.go:83
// Called from: detail.go:47-77 (in detailView)
// Renders a single label-value row for the detail screen with styled label
// (DetailLabel) and value (DetailValue).
func detailRow(label, value string) string {
	return fmt.Sprintf("  %s %s", DetailLabel.Render(label), DetailValue.Render(value))
}

// formatContext — internal/tui/detail.go:87
// Called from: detail.go:52; benchmarks.go:113; results.go:466
// Formats a context length integer with suffix: "M" for millions,
// "k" for thousands, raw number otherwise.
func formatContext(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.0fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// formatNum — internal/tui/detail.go:97
// Called from: detail.go:76-77 (in detailView)
// Formats a large integer with suffix: "M" for millions (1 decimal),
// "k" for thousands (1 decimal), raw number otherwise.
func formatNum(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
