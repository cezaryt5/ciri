package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/predictor"

	tea "github.com/charmbracelet/bubbletea"
)

type benchmarksModel struct {
	selected  predictor.ModelPrediction
	gpu       *hardware.GPU
	benchDB   *predictor.BenchmarkDB
	rows      []predictor.BenchmarkRow
	closestHW string
	specs     hardware.Specs
	scroll    int
}

func newBenchmarksModel(selected predictor.ModelPrediction, gpu *hardware.GPU, benchDB *predictor.BenchmarkDB, specs hardware.Specs) *benchmarksModel {
	var rows []predictor.BenchmarkRow
	closest := ""

	if benchDB != nil && gpu != nil {
		ids := selected.Model.BenchmarkIDs
		if len(ids) == 0 {
			ids = []string{selected.Model.Name}
		}
		keys := []string{strings.ToLower(gpu.CanonicalName), strings.ToLower(gpu.Name)}
		seen := map[string]bool{}
		for _, id := range ids {
			hfID := strings.ToLower(id)
			for _, gpuKey := range keys {
				for _, r := range benchDB.ByNameHfID()[gpuKey+"|"+hfID] {
					sig := fmt.Sprintf("%s|%s|%s|%.1f|%d",
						r.HfID, r.EngineName, r.Quantization, r.TokSOut, r.ContextLen)
					if seen[sig] {
						continue
					}
					seen[sig] = true
					rows = append(rows, r)
					closest = gpu.Name
				}
			}
		}
	}

	return &benchmarksModel{
		selected:  selected,
		gpu:       gpu,
		benchDB:   benchDB,
		rows:      rows,
		closestHW: closest,
		specs:     specs,
	}
}

func (bm *benchmarksModel) benchUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		target := a.benchOrigin
		if target == screenBenchmarks {
			target = screenExplore
		}
		return func() tea.Msg { return navigateMsg{target: target} }
	case "up", "k":
		if bm.scroll > 0 {
			bm.scroll--
		}
	case "down", "j":
		if bm.scroll < len(bm.rows)-1 {
			bm.scroll++
		}
	}
	return nil
}

func (bm *benchmarksModel) benchView(a *App) string {
	inner := a.width - 6
	if inner < 22 {
		inner = 22
	}

	var b strings.Builder

	b.WriteString("\n")
	match := "NONE"
	if bm.closestHW != "" {
		match = bm.closestHW
	}
	metaLeft := Label.Render("CLOSEST HARDWARE") + "  " + Value.Render(truncate(match, 26))
	modelLabel := "  " + Label.Render("MODEL") + "  "
	nameW := inner - lipgloss.Width(metaLeft) - lipgloss.Width(modelLabel)
	if nameW < 8 {
		nameW = 8
	}
	name := bm.selected.Model.Display
	if name == "" {
		name = bm.selected.Model.Name
	}
	b.WriteString("  " + metaLeft + modelLabel +
		Value.Render(truncate(name, nameW)) + "\n")
	b.WriteString("\n")

	if len(bm.rows) == 0 {
		b.WriteString("  " + statusSquare(false) + " " +
			Value.Render("NO BENCHMARKS ON COMPARABLE HARDWARE") + "\n")
		b.WriteString("\n")
		b.WriteString("  " + Label.Render("FALLBACK") + "  " +
			Tagline.Render("TFLOPS HEURISTIC") + "\n")
		if bm.gpu != nil && bm.gpu.Architecture != "" {
			b.WriteString("  " + Label.Render("GPU CLASS") + "  " +
				Value.Render(bm.gpu.Architecture) + "\n")
		}
		return b.String()
	}

	w := benchColWidths(inner)
	b.WriteString(renderBenchHeader(w) + "\n")
	b.WriteString("  " + RenderRule(inner) + "\n")

	vis := bmVisibleRows(a)
	end := bm.scroll + vis
	if end > len(bm.rows) {
		end = len(bm.rows)
	}

	if bm.scroll > 0 {
		b.WriteString("  " + Tagline.Render(fmt.Sprintf("\u2191 %d ABOVE", bm.scroll)) + "\n")
	} else {
		b.WriteString("\n")
	}

	for i := bm.scroll; i < end; i++ {
		b.WriteString(renderBenchRow(bm.rows[i], w) + "\n")
	}

	if remaining := len(bm.rows) - end; remaining > 0 {
		b.WriteString("  " + Tagline.Render(fmt.Sprintf("\u2193 %d MORE", remaining)) + "\n")
	} else {
		b.WriteString("\n")
	}

	return b.String()
}

type benchCols struct {
	engine int
	tokS   int
	vram   int
	ctx    int
	notes  int
}

func benchColWidths(inner int) benchCols {
	c := benchCols{engine: 16, tokS: 8, vram: 8, ctx: 9}
	c.notes = inner - c.engine - c.tokS - c.vram - c.ctx
	if c.notes < 12 {
		c.notes = 12
	}
	return c
}

func renderBenchHeader(w benchCols) string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(renderCell(Label.Render("ENGINE"), w.engine, lipgloss.Left))
	b.WriteString(renderCell(Label.Render("TOK/S"), w.tokS, lipgloss.Right))
	b.WriteString(renderCell(Label.Render("VRAM"), w.vram, lipgloss.Right))
	b.WriteString(renderCell(Label.Render("CONTEXT"), w.ctx, lipgloss.Right))
	b.WriteString(renderCell(Label.Render("NOTES"), w.notes, lipgloss.Left))
	return b.String()
}

func renderBenchRow(r predictor.BenchmarkRow, w benchCols) string {
	vram := "\u2014"
	if r.PeakVRAMGB > 0 {
		vram = fmt.Sprintf("%.1fG", r.PeakVRAMGB)
	}
	ctx := "\u2014"
	if r.ContextLen > 0 {
		ctx = formatContext(r.ContextLen)
	}

	notes := r.Notes
	if notes == "" {
		notes = strings.TrimSpace(r.EngineName + " · " + r.Quantization)
	}

	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(renderCell(Value.Render(truncate(r.EngineName, w.engine-1)), w.engine, lipgloss.Left))
	b.WriteString(renderCell(Value.Render(fmt.Sprintf("%.1f", r.TokSOut)), w.tokS, lipgloss.Right))
	b.WriteString(renderCell(Tagline.Render(vram), w.vram, lipgloss.Right))
	b.WriteString(renderCell(Tagline.Render(ctx), w.ctx, lipgloss.Right))
	b.WriteString(renderCell(Tagline.Render(truncate(notes, w.notes-1)), w.notes, lipgloss.Left))
	return b.String()
}

func bmVisibleRows(a *App) int {
	n := a.height - 14
	if n < 1 {
		return 1
	}
	return n
}
