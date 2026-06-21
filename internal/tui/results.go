package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
	"github.com/cezaryt5/Can_I_Run_IT/internal/model"
	"github.com/cezaryt5/Can_I_Run_IT/internal/predictor"

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

// newResultsModel — internal/tui/results.go:53
// Called from: app.go:122 (in App.View)
// Creates a new resultsModel by running Predict for the given category and
// applying initial search/filter/sort defaults.
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

// applyFilters — internal/tui/results.go:66
// Called from: results.go:60 (in newResultsModel), 135,139,143,146,160,168,171 (in resultsUpdate)
// Filters r.allPredictions by fit status and search query, then sorts by
// the active sort mode. Clamps cursor and scroll offset to valid bounds.
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

// resultsUpdate — internal/tui/results.go:126
// Called from: app.go:100 (in App.Update)
// Handles all keyboard input on the results screen:
//   - Search mode: captures typing, backspace, enter/esc
//   - Normal mode: navigation (↑↓), search (/), fit filter (f), sort (s),
//     enter detail (enter), benchmarks (b), clear filters then go home (esc)
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

// resultsView — internal/tui/results.go:218
// Called from: app.go:124 (in App.View)
// Renders the results table: search/filter bar, column headers, divider,
// scroll-up indicator, visible data rows, scroll-down indicator. Only
// renders rows within the visible viewport.
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
// searchBar — internal/tui/results.go:270
// Called from: results.go:224 (in resultsView)
// Renders a compact control bar with four mini-boxes: Search ([/] key),
// Sort ([s] key), Fit filter ([f] key), and count ("Shown"). Active
// controls are highlighted.
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

// renderMiniBox — internal/tui/results.go:296
// Called from: results.go:282-286 (in searchBar)
// Draws a one-line rounded control box with title and value. Uses unicode
// box-drawing characters (╭─╮). Active state is highlighted with a bold
// cyan style.
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

// fitFilterLabel — internal/tui/results.go:313
// Called from: results.go:285 (in searchBar)
// Returns the display label for the current fit filter state: All, Perfect,
// Good, or Slow.
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

// sortModeLabel — internal/tui/results.go:326
// Called from: results.go:284 (in searchBar)
// Returns the display label for the current sort mode: Default, Speed,
// Size, or Name.
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

// resultsPreview — internal/tui/results.go:339
// Called from: app.go:126 (in App.View)
// Renders a preview bar showing the currently selected model's name,
// parameter count, and provider.
func (r *resultsModel) resultsPreview() string {
	selected := r.predictions[r.cursor]
	return PreviewBar.Render(fmt.Sprintf("  ▶ %s  %s  %s",
		truncate(selected.Model.Name, 50),
		selected.Model.ParameterCount,
		selected.Model.Provider))
}

// resultsFooter — internal/tui/results.go:347
// Called from: app.go:128 (in App.View)
// Renders the footer with keyboard shortcuts. Shows search instructions
// when in search mode, or full navigation help otherwise.
func (r *resultsModel) resultsFooter() string {
	if r.searching {
		return Footer.Render("  Type to search  Enter Apply  Esc Clear")
	}
	return Footer.Render("  ↑↓ Navigate  / Search  F Fit  S Sort  Enter Details  B Benchmarks  Esc Back")
}

// visibleRows — internal/tui/results.go:354
// Called from: results.go:183,236 (in resultsUpdate and resultsView)
// Computes the number of table rows that fit in the terminal (accounting
// for control bar, header, divider, scroll indicators, and borders).
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

// columnWidths — internal/tui/results.go:378
// Called from: results.go:219 (in resultsView)
// Computes dynamic column widths for the results table based on terminal
// width. Ensures the model name column gets remaining space after fixed
// columns. Clamps provider and model width to minimums.
func columnWidths(termWidth int) colWidths {
	innerWidth := termWidth - 2
	dot := 4
	provider := 20
	params, toks, quant, disk, mode, mem, ctx, date, fit := 7, 7, 8, 6, 5, 5, 6, 8, 8
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

// renderTableHeader — internal/tui/results.go:399
// Called from: results.go:227 (in resultsView)
// Renders the column header row of the results table with padded labels
// (●, Model, Provider, Params, tok/s, Quant, Disk, Mode, Mem%, Ctx, Date,
// Fit). Uses the TableHeader bold style.
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

// renderTableRow — internal/tui/results.go:416
// Called from: results.go:253 (in resultsView)
// Renders a single prediction row with all columns. Selected rows use
// reverse video. Memory percentage is color-coded (green < 50 %, yellow
// ≤ 80 %, red > 80 %). Fit status is shown with colored dots and labels.
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
		{speed, w.tokS},
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

// padCell — internal/tui/results.go:484
// Called from: results.go:401-412 (in renderTableHeader)
// Pads text to a fixed width using lipgloss style. If width ≤ 0, returns
// the text unchanged.
func padCell(text string, width int) string {
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().Width(width).Render(text)
}

// fitDotStr — internal/tui/results.go:491
// Called from: results.go:439 (in renderTableRow)
// Returns a colored dot symbol (●) for the fit status: green for
// Recommended, yellow for Advanced, uncolored for TooHeavy.
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

// fitLabel — internal/tui/results.go:502
// Called from: results.go:440 (in renderTableRow)
// Returns a colored text label for the fit status: green "Perfect",
// yellow "Good", or uncolored "Slow".
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

// fitLabelPlain — internal/tui/results.go:513
// Called from: results.go:443 (in renderTableRow, selected row)
// Returns an uncolored text label for fit status (Perfect / Good / Slow).
// Used on the selected row to keep the reverse-video highlight clean.
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

// formatMemPctRaw — internal/tui/results.go:524
// Called from: results.go:422 (in renderTableRow)
// Calculates the percentage of GPU VRAM that the model's MinVRAMGB
// consumes. Caps at 100 %. Returns "—" if GPU or VRAM data is unavailable.
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

// formatDiskGB — internal/tui/results.go:535
// Called from: results.go:463 (in renderTableRow)
// Estimates the model's disk footprint: params × bytesPerParam. Returns
// a string like "7.5G" or "0.08G". Returns "—" if params are unknown.
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

// formatMode — internal/tui/results.go:547
// Called from: results.go:464 (in renderTableRow)
// Returns "GPU" for Recommended fit (fits in VRAM) or "CPU" for Advanced
// fit (spills to system RAM).
func formatMode(fit predictor.FitStatus) string {
	if fit == predictor.Recommended {
		return "GPU"
	}
	return "CPU"
}

// formatDate — internal/tui/results.go:554
// Called from: results.go:467 (in renderTableRow)
// Formats a release date string to "YYYY-MM" (first 7 chars). Returns
// "—" if the date is empty or too short.
func formatDate(date string) string {
	if date == "" || len(date) < 7 {
		return "\u2014"
	}
	return date[:7]
}

// truncate — internal/tui/results.go:561
// Called from: app.go:166,171; results.go:458-467; styles.go:112; benchmarks.go:70,120; detail.go:43
// Truncates a string to max characters, appending "…" if truncated.
// Returns "" if max ≤ 0.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "\u2026"
}
