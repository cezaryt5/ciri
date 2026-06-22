package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"

	tea "github.com/charmbracelet/bubbletea"
)

// fitFilter narrows the list to a single fit status (or all).
type fitFilter int

const (
	fitAll fitFilter = iota
	fitRecommended
	fitAdvanced
	fitTooHeavy
)

// sortMode controls the ordering of the visible list.
type sortMode int

const (
	sortDefault sortMode = iota
	sortSpeed
	sortSize
	sortName
)

type resultsModel struct {
	allPredictions []predictor.ModelPrediction
	predictions    []predictor.ModelPrediction
	cursor         int
	scrollOff      int
	category       model.Category
	specs          hardware.Specs
	gpu            *hardware.GPU

	// search / filter state (llmfit-style)
	searching   bool
	searchQuery string
	fit         fitFilter
	sort        sortMode
}

func newResultsModel(pred *predictor.Predictor, cat model.Category, specs hardware.Specs, gpu *hardware.GPU) *resultsModel {
	r := &resultsModel{
		allPredictions: pred.Predict(cat),
		category:       cat,
		specs:          specs,
		gpu:            gpu,
	}
	r.applyFilters()
	return r
}

// applyFilters rebuilds r.predictions from r.allPredictions using the active
// search query, fit filter and sort mode, then clamps the cursor/scroll.
func (r *resultsModel) applyFilters() {
	q := strings.ToLower(strings.TrimSpace(r.searchQuery))

	filtered := make([]predictor.ModelPrediction, 0, len(r.allPredictions))
	for _, p := range r.allPredictions {
		switch r.fit {
		case fitRecommended:
			if p.FitStatus != predictor.Recommended {
				continue
			}
		case fitAdvanced:
			if p.FitStatus != predictor.Advanced {
				continue
			}
		case fitTooHeavy:
			if p.FitStatus != predictor.TooHeavy {
				continue
			}
		}
		if q != "" {
			hay := strings.ToLower(p.Model.Name + " " + p.Model.Provider + " " +
				p.Model.Quantization + " " + p.Model.ParameterCount)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		filtered = append(filtered, p)
	}

	switch r.sort {
	case sortSpeed:
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].EstTokPerSec > filtered[j].EstTokPerSec
		})
	case sortSize:
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].Model.ParametersRaw < filtered[j].Model.ParametersRaw
		})
	case sortName:
		sort.SliceStable(filtered, func(i, j int) bool {
			return strings.ToLower(filtered[i].Model.Name) < strings.ToLower(filtered[j].Model.Name)
		})
	}

	r.predictions = filtered

	if r.cursor >= len(filtered) {
		r.cursor = len(filtered) - 1
	}
	if r.cursor < 0 {
		r.cursor = 0
	}
	if r.scrollOff > r.cursor {
		r.scrollOff = r.cursor
	}
	if r.scrollOff < 0 {
		r.scrollOff = 0
	}
}

func (r *resultsModel) resultsUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	// Search input mode captures all typing until Enter/Esc.
	if r.searching {
		switch msg.Type {
		case tea.KeyEnter:
			r.searching = false
		case tea.KeyEsc:
			r.searching = false
			r.searchQuery = ""
			r.applyFilters()
		case tea.KeyBackspace:
			if len(r.searchQuery) > 0 {
				r.searchQuery = r.searchQuery[:len(r.searchQuery)-1]
				r.applyFilters()
			}
		case tea.KeySpace:
			r.searchQuery += " "
			r.applyFilters()
		case tea.KeyRunes:
			r.searchQuery += string(msg.Runes)
			r.applyFilters()
		}
		return nil
	}

	n := len(r.predictions)

	switch msg.String() {
	case "esc":
		// First Esc clears an active search/filter; a clean list goes home.
		if r.searchQuery != "" || r.fit != fitAll || r.sort != sortDefault {
			r.searchQuery = ""
			r.fit = fitAll
			r.sort = sortDefault
			r.applyFilters()
			return nil
		}
		return func() tea.Msg { return navigateMsg{target: screenHome} }
	case "/":
		r.searching = true
	case "f":
		r.fit = (r.fit + 1) % 4
		r.applyFilters()
	case "s":
		r.sort = (r.sort + 1) % 4
		r.applyFilters()
	case "up", "k":
		if r.cursor > 0 {
			r.cursor--
		}
		if r.cursor < r.scrollOff {
			r.scrollOff = r.cursor
		}
	case "down", "j":
		if r.cursor < n-1 {
			r.cursor++
		}
		vis := visibleRows(a)
		if r.cursor >= r.scrollOff+vis {
			r.scrollOff = r.cursor - vis + 1
		}
	case "enter":
		if r.cursor >= 0 && r.cursor < n {
			a.detail = &detailModel{
				selected: r.predictions[r.cursor],
				category: r.category,
				specs:    r.specs,
				gpu:      r.gpu,
			}
			return func() tea.Msg { return navigateMsg{target: screenDetail} }
		}
	case "b":
		if r.cursor >= 0 && r.cursor < n {
			a.detail = &detailModel{
				selected: r.predictions[r.cursor],
				category: r.category,
				specs:    r.specs,
				gpu:      r.gpu,
			}
			return func() tea.Msg { return navigateMsg{target: screenBenchmarks} }
		}
	}

	if r.scrollOff > n-1 {
		r.scrollOff = n - 1
	}
	if r.scrollOff < 0 {
		r.scrollOff = 0
	}
	return nil
}

func (r *resultsModel) resultsView(a *App) string {
	w := columnWidths(a.width)

	var b strings.Builder

	// Search / filter bar (always shown so the active query/filters are visible).
	b.WriteString(r.searchBar(a.width-2) + "\n")

	// Column header
	b.WriteString(renderTableHeader(w) + "\n")
	b.WriteString(RenderDivider(a.width-2) + "\n")

	n := len(r.predictions)
	if n == 0 {
		b.WriteString("  No models match your search/filter.\n")
		return b.String()
	}

	vis := visibleRows(a)
	end := r.scrollOff + vis
	if end > n {
		end = n
	}

	// Always reserve the indicator line (blank when there's nothing above) so
	// the header and rows stay fixed in place while scrolling.
	if r.scrollOff > 0 {
		fmt.Fprintf(&b, "  ↑ %d above\n", r.scrollOff)
	} else {
		b.WriteString("\n")
	}

	// Data rows
	for i := r.scrollOff; i < end; i++ {
		selected := r.cursor == i
		b.WriteString(renderTableRow(r.predictions[i], r.gpu, w, selected, a.width) + "\n")
	}

	// Reserve the trailing indicator line too, for the same reason.
	remaining := n - end
	if remaining > 0 {
		fmt.Fprintf(&b, "  ↓ %d more\n", remaining)
	} else {
		b.WriteString("\n")
	}

	return b.String()
}

// searchBar renders compact llmfit-style boxed controls left-to-right. Keep
// controls fixed-width instead of letting Search expand and push Sort/Fit to
// the far right; llmfit places filters immediately after Search.
func (r *resultsModel) searchBar(innerWidth int) string {
	query := r.searchQuery
	if query == "" {
		if r.searching {
			query = "type to search... \u2588"
		} else {
			query = "Press / to search..."
		}
	} else if r.searching {
		query += "\u2588"
	}

	searchBox := renderMiniBox("Search [/]", query, 38,
		r.searching || r.searchQuery != "")
	sortBox := renderMiniBox("Sort [s]", sortModeLabel(r.sort), 22, r.sort != sortDefault)
	fitBox := renderMiniBox("Fit [f]", fitFilterLabel(r.fit), 18, r.fit != fitAll)
	countBox := renderMiniBox("Shown", fmt.Sprintf("%d/%d", len(r.predictions), len(r.allPredictions)), 18, false)

	line := searchBox + " " + sortBox + " " + fitBox + " " + countBox
	if lipgloss.Width(line) < innerWidth {
		line += repeat(" ", innerWidth-lipgloss.Width(line))
	}
	return line
}

// renderMiniBox draws a one-line rounded control. totalWidth includes borders.
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

func sortModeLabel(s sortMode) string {
	switch s {
	case sortSpeed:
		return "Speed"
	case sortSize:
		return "Size"
	case sortName:
		return "Name"
	default:
		return "Default"
	}
}

func (r *resultsModel) resultsPreview() string {
	selected := r.predictions[r.cursor]
	return PreviewBar.Render(fmt.Sprintf("  ▶ %s  %s  %s",
		truncate(selected.Model.Name, 50),
		selected.Model.ParameterCount,
		selected.Model.Provider))
}

func (r *resultsModel) resultsFooter() string {
	if r.searching {
		return Footer.Render("  Type to search  Enter Apply  Esc Clear")
	}
	return Footer.Render("  ↑↓ Navigate  / Search  F Fit  S Sort  Enter Details  B Benchmarks  Esc Back")
}

func visibleRows(a *App) int {
	// Account for the compact control bar, table header, divider and the two
	// always-reserved scroll-indicator lines.
	n := a.height - 15
	if n < 1 {
		return 1
	}
	return n
}

type colWidths struct {
	model    int
	provider int
	params   int
	tokS     int
	quant    int
	disk     int
	mode     int
	mem      int
	ctx      int
	date     int
	fit      int
}

func columnWidths(termWidth int) colWidths {
	innerWidth := termWidth - 2
	dot := 4
	provider := 25
	params, toks, quant, disk, mode, mem, ctx, date, fit := 7, 7, 10, 6, 5, 5, 6, 8, 8
	fixedOther := params + toks + quant + disk + mode + mem + ctx + date + fit

	modelW := innerWidth - dot - provider - fixedOther
	if modelW < 15 {
		provider = innerWidth - dot - 15 - fixedOther
		if provider < 10 {
			provider = 10
		}
		modelW = innerWidth - dot - provider - fixedOther
		if modelW < 12 {
			modelW = 12
		}
	}
	return colWidths{modelW, provider, params, toks, quant, disk, mode, mem, ctx, date, fit}
}

func renderTableHeader(w colWidths) string {
	return TableHeader.Render(
		padCell("  ●", 4) +
			padCell("Model", w.model) +
			padCell("Provider", w.provider) +
			padCell("Params", w.params) +
			padCell("tok/s", w.tokS) +
			padCell("Quant", w.quant) +
			padCell("Disk", w.disk) +
			padCell("Mode", w.mode) +
			padCell("Mem%", w.mem) +
			padCell("Ctx", w.ctx) +
			padCell("Date", w.date) +
			padCell("Fit", w.fit),
	)
}

func renderTableRow(p predictor.ModelPrediction, gpu *hardware.GPU, w colWidths, selected bool, termWidth int) string {
	speed := fmt.Sprintf("%.0f", p.EstTokPerSec)
	if p.FitStatus == predictor.Advanced {
		speed = "~" + fmt.Sprintf("%.0f", p.EstTokPerSec)
	}

	memPct := formatMemPctRaw(&p.Model, gpu)
	// For a selected row we keep cells uncolored so the reverse-video
	// highlight stays clean and uniform across the whole line.
	coloredMem := memPct
	if !selected && memPct != "\u2014" {
		var v float64
		fmt.Sscanf(memPct, "%f%%", &v)
		switch {
		case v < 50:
			coloredMem = MemPctGood.Render(memPct)
		case v <= 80:
			coloredMem = MemPctWarn.Render(memPct)
		default:
			coloredMem = MemPctBad.Render(memPct)
		}
	}

	dot := fitDotStr(p.FitStatus)
	fit := fitLabel(p.FitStatus)
	if selected {
		dot = "\u25cf"
		fit = fitLabelPlain(p.FitStatus)
	}

	// Keep the status dot in the same column whether or not the row is
	// selected; only the leading marker cell changes (space vs. triangle).
	dotText := "  " + dot
	if selected {
		dotText = "\u25b6 " + dot
	}

	cells := []struct {
		text  string
		width int
	}{
		{dotText, 4},
		{truncate(p.Model.Name, w.model), w.model},
		{truncate(p.Model.Provider, w.provider), w.provider},
		{truncate(p.Model.ParameterCount, w.params), w.params},
		{truncate(speed, w.tokS), w.tokS},
		{truncate(p.Model.Quantization, w.quant), w.quant},
		{truncate(formatDiskGB(&p.Model), w.disk), w.disk},
		{truncate(formatMode(p.FitStatus), w.mode), w.mode},
		{coloredMem, w.mem},
		{truncate(formatContext(p.Model.ContextLength), w.ctx), w.ctx},
		{truncate(formatDate(p.Model.ReleaseDate), w.date), w.date},
		{fit, w.fit},
	}

	var line string
	for _, c := range cells {
		if c.width > 0 {
			line += lipgloss.NewStyle().Width(c.width).Render(c.text)
		}
	}

	if selected {
		return SelectedRow.Width(termWidth - 2).Render(line)
	}
	return line
}

func padCell(text string, width int) string {
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().Width(width).Render(text)
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

func formatMemPctRaw(m *model.Model, gpu *hardware.GPU) string {
	if gpu == nil || gpu.VRAMGB <= 0 || m.MinVRAMGB <= 0 {
		return "\u2014"
	}
	pct := m.MinVRAMGB / gpu.VRAMGB * 100
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("%.0f%%", pct)
}

func formatDiskGB(m *model.Model) string {
	if m.ParametersRaw <= 0 {
		return "\u2014"
	}
	bytesPerParam := predictor.BytesPerParam(m.Quantization)
	diskGB := float64(m.ParametersRaw) / 1e9 * bytesPerParam
	if diskGB < 0.1 {
		return fmt.Sprintf("%.2fG", diskGB)
	}
	return fmt.Sprintf("%.1fG", diskGB)
}

func formatMode(fit predictor.FitStatus) string {
	if fit == predictor.Recommended {
		return "GPU"
	}
	return "CPU"
}

func formatDate(date string) string {
	if date == "" || len(date) < 7 {
		return "\u2014"
	}
	return date[:7]
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "\u2026"
}
