package tui

import (
	"fmt"
	"strings"

	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
	"github.com/cezaryt5/Can_I_Run_IT/internal/predictor"

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
		canonical := strings.ToLower(gpu.CanonicalName)
		hfID := strings.ToLower(selected.Model.Name)
		key := canonical + "|" + hfID
		if r, ok := benchDB.ByNameHfID()[key]; ok {
			rows = r
			closest = gpu.Name
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
			target = screenResults
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
	var b strings.Builder

	name := truncate(bm.selected.Model.Name, 45)
	b.WriteString(Title.Render("  Benchmarks: "+name) + "\n\n")

	if bm.closestHW != "" {
		b.WriteString(fmt.Sprintf("  Benchmark Results (closest hardware match: %s)\n\n", bm.closestHW))
	} else {
		b.WriteString("  Benchmark Results\n\n")
	}

	if len(bm.rows) == 0 {
		b.WriteString("  No benchmarks available for this model on comparable hardware.\n")
		if bm.gpu != nil {
			b.WriteString(fmt.Sprintf("  * Estimated from TFLOPS heuristic: %s class\n", bm.gpu.Architecture))
		}
	} else {
		vis := bmVisibleRows(a)
		end := bm.scroll + vis
		if end > len(bm.rows) {
			end = len(bm.rows)
		}

		if bm.scroll > 0 {
			b.WriteString(fmt.Sprintf("  ↑ %d above\n", bm.scroll))
		}

		b.WriteString(TableHeader.Render(
			fmt.Sprintf("  %-15s %8s %8s %10s   Notes", "Engine", "tok/s", "VRAM", "Context"),
		) + "\n")
		b.WriteString(RenderDivider(a.width-2) + "\n")

		for i := bm.scroll; i < end; i++ {
			row := bm.rows[i]
			prefix := "  "
			if i == bm.scroll && bm.scroll > 0 {
				prefix = Cursor.Render("  ") // Not a cursor, just spacing
			}

			vramStr := "\u2014"
			if row.PeakVRAMGB > 0 {
				vramStr = fmt.Sprintf("%.1f GB", row.PeakVRAMGB)
			}
			ctxStr := "\u2014"
			if row.ContextLen > 0 {
				ctxStr = formatContext(row.ContextLen)
			}

			notes := ""
			if row.Notes != "" {
				notes = truncate(row.Notes, 40)
			} else {
				notes = fmt.Sprintf("%s, %s", row.EngineName, row.Quantization)
			}

			line := fmt.Sprintf("%s %-15s %8.1f %8s %10s   %s",
				prefix, truncate(row.EngineName, 15),
				row.TokSOut, vramStr, ctxStr,
				notes,
			)
			b.WriteString(ModelName.Render(line) + "\n")
		}

		remaining := len(bm.rows) - end
		if remaining > 0 {
			b.WriteString(fmt.Sprintf("  ↓ %d more\n", remaining))
		}
	}

	b.WriteString("\n" + Footer.Render("  Esc Back"))
	return b.String()
}

func bmVisibleRows(a *App) int {
	n := a.height - 14
	if n < 1 {
		return 1
	}
	return n
}
