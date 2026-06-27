package tui

import (
	"fmt"
	"strings"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/predictor"

	tea "github.com/charmbracelet/bubbletea"
)

type detailModel struct {
	selected predictor.ModelPrediction
	specs    hardware.Specs
	gpu      *hardware.GPU
}

func newDetailModel(pred *predictor.Predictor, specs hardware.Specs, gpu *hardware.GPU) *detailModel {
	return &detailModel{
		specs: specs,
		gpu:   gpu,
	}
}

func (d *detailModel) detailUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return func() tea.Msg { return navigateMsg{target: screenExplore} }
	case "b":
		return func() tea.Msg { return navigateMsg{target: screenBenchmarks} }
	}
	return nil
}

func (d *detailModel) detailView(a *App) string {
	m := d.selected.Model
	var b strings.Builder

	name := truncate(m.Name, 50)
	b.WriteString(Title.Render("  " + name) + "\n\n")

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

	if d.gpu != nil && d.gpu.VRAMGB > 0 {
		needed := predictor.ModelVRAMRequirement(m)
		if needed > 0 {
			pct := needed / d.gpu.VRAMGB * 100
			b.WriteString(detailRow("  VRAM Usage", fmt.Sprintf("%.0f%%", pct)) + "\n")
		}
	}

	b.WriteString("\n  Community\n")
	b.WriteString(detailRow("  Downloads", formatNum(m.HfDownloads)) + "\n")
	b.WriteString(detailRow("  Likes", formatNum(m.HfLikes)) + "\n")

	b.WriteString("\n" + Footer.Render("  Esc Back  B Benchmarks"))
	return b.String()
}

func detailRow(label, value string) string {
	return fmt.Sprintf("  %s %s", DetailLabel.Render(label), DetailValue.Render(value))
}

func formatContext(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.0fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func formatNum(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
