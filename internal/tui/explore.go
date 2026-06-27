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

// filterMinParams is the minimum parameter count (500M) shown on the explore
// screen. Models below this threshold are considered test artifacts.
const filterMinParams int64 = 500_000_000

type sortField int

const (
	sortByName sortField = iota
	sortByParams
	sortBySpeed
	sortByDisk
	sortByDate
	sortByFit
	sortFieldCount
)

var sortFieldLabels = [sortFieldCount]string{
	"Name", "Params", "Speed", "Disk", "Date", "Fit",
}

type fitFilter int

const (
	fitAll fitFilter = iota
	fitRecommended
	fitAdvanced
	fitTooHeavy
)

type exploreModel struct {
	allPredictions []predictor.ModelPrediction
	predictions    []predictor.ModelPrediction
	cursor         int
	scrollOff      int
	specs          hardware.Specs
	gpu            *hardware.GPU

	searching   bool
	searchQuery string
	fit         fitFilter
	sortF       sortField
	sortAsc     bool
	typeFilter  *model.Category
}

func newExploreModel(pred *predictor.Predictor, specs hardware.Specs, gpu *hardware.GPU) *exploreModel {
	all := pred.PredictAll()
	filtered := make([]predictor.ModelPrediction, 0, len(all))
	for _, p := range all {
		if p.Model.ParametersRaw >= filterMinParams {
			filtered = append(filtered, p)
		}
	}
	em := &exploreModel{
		allPredictions: filtered,
		specs:          specs,
		gpu:            gpu,
		sortF:          sortByName,
		sortAsc:        true,
	}
	em.applyFilters()
	return em
}

func (em *exploreModel) applyFilters() {
	q := strings.ToLower(strings.TrimSpace(em.searchQuery))

	filtered := make([]predictor.ModelPrediction, 0, len(em.allPredictions))
	for _, p := range em.allPredictions {
		switch em.fit {
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
		if em.typeFilter != nil {
			if !hasTypeCategory(p.Model, *em.typeFilter) {
				continue
			}
		}
		if q != "" {
			hay := strings.ToLower(p.Model.Name + " " +
				p.Model.Quantization + " " + p.Model.ParameterCount)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		filtered = append(filtered, p)
	}

	switch em.sortF {
	case sortBySpeed:
		sort.SliceStable(filtered, func(i, j int) bool {
			if em.sortAsc {
				return filtered[i].EstTokPerSec < filtered[j].EstTokPerSec
			}
			return filtered[i].EstTokPerSec > filtered[j].EstTokPerSec
		})
	case sortByParams:
		sort.SliceStable(filtered, func(i, j int) bool {
			if em.sortAsc {
				return filtered[i].Model.ParametersRaw < filtered[j].Model.ParametersRaw
			}
			return filtered[i].Model.ParametersRaw > filtered[j].Model.ParametersRaw
		})
	case sortByDisk:
		sort.SliceStable(filtered, func(i, j int) bool {
			ai := predictor.ModelWeightSizeGB(filtered[i].Model)
			aj := predictor.ModelWeightSizeGB(filtered[j].Model)
			if em.sortAsc {
				return ai < aj
			}
			return ai > aj
		})
	case sortByDate:
		sort.SliceStable(filtered, func(i, j int) bool {
			if em.sortAsc {
				return filtered[i].Model.ReleaseDate < filtered[j].Model.ReleaseDate
			}
			return filtered[i].Model.ReleaseDate > filtered[j].Model.ReleaseDate
		})
	case sortByFit:
		sort.SliceStable(filtered, func(i, j int) bool {
			if em.sortAsc {
				return filtered[i].FitStatus < filtered[j].FitStatus
			}
			return filtered[i].FitStatus > filtered[j].FitStatus
		})
	case sortByName:
		sort.SliceStable(filtered, func(i, j int) bool {
			if em.sortAsc {
				return strings.ToLower(filtered[i].Model.Name) < strings.ToLower(filtered[j].Model.Name)
			}
			return strings.ToLower(filtered[i].Model.Name) > strings.ToLower(filtered[j].Model.Name)
		})
	}

	em.predictions = filtered

	if em.cursor >= len(filtered) {
		em.cursor = len(filtered) - 1
	}
	if em.cursor < 0 {
		em.cursor = 0
	}
	if em.scrollOff > em.cursor {
		em.scrollOff = em.cursor
	}
	if em.scrollOff < 0 {
		em.scrollOff = 0
	}
}

func (em *exploreModel) exploreUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	if em.searching {
		switch msg.Type {
		case tea.KeyEnter:
			em.searching = false
		case tea.KeyEsc:
			em.searching = false
			em.searchQuery = ""
			em.applyFilters()
		case tea.KeyBackspace:
			if len(em.searchQuery) > 0 {
				em.searchQuery = em.searchQuery[:len(em.searchQuery)-1]
				em.applyFilters()
			}
		case tea.KeySpace:
			em.searchQuery += " "
			em.applyFilters()
		case tea.KeyRunes:
			em.searchQuery += string(msg.Runes)
			em.applyFilters()
		}
		return nil
	}

	n := len(em.predictions)

	switch msg.String() {
	case "esc":
		if em.searchQuery != "" || em.fit != fitAll || em.sortF != sortByName || !em.sortAsc || em.typeFilter != nil {
			em.searchQuery = ""
			em.fit = fitAll
			em.sortF = sortByName
			em.sortAsc = true
			em.typeFilter = nil
			em.applyFilters()
			return nil
		}
		return func() tea.Msg { return navigateMsg{target: screenHome} }
	case "/":
		em.searching = true
	case "f":
		em.fit = (em.fit + 1) % 4
		em.applyFilters()
	case "s":
		em.sortF = (em.sortF + 1) % sortFieldCount
		em.applyFilters()
	case "A":
		em.sortAsc = true
		em.applyFilters()
	case "D":
		em.sortAsc = false
		em.applyFilters()
	case "t":
		em.typeFilter = cycleTypeFilter(em.typeFilter)
		em.applyFilters()
	case "up", "k":
		if em.cursor > 0 {
			em.cursor--
		}
		if em.cursor < em.scrollOff {
			em.scrollOff = em.cursor
		}
	case "down", "j":
		if em.cursor < n-1 {
			em.cursor++
		}
		vis := emVisibleRows(a)
		if em.cursor >= em.scrollOff+vis {
			em.scrollOff = em.cursor - vis + 1
		}
	case "enter":
		if em.cursor >= 0 && em.cursor < n {
			a.detail = &detailModel{
				selected: em.predictions[em.cursor],
				specs:    em.specs,
				gpu:      em.gpu,
			}
			return func() tea.Msg { return navigateMsg{target: screenDetail} }
		}
	case "b":
		if em.cursor >= 0 && em.cursor < n {
			a.detail = &detailModel{
				selected: em.predictions[em.cursor],
				specs:    em.specs,
				gpu:      em.gpu,
			}
			return func() tea.Msg { return navigateMsg{target: screenBenchmarks} }
		}
	}

	if em.scrollOff > n-1 {
		em.scrollOff = n - 1
	}
	if em.scrollOff < 0 {
		em.scrollOff = 0
	}
	return nil
}

func (em *exploreModel) exploreView(a *App) string {
	w := exploreColWidths(a.width)

	var b strings.Builder

	b.WriteString(em.exploreSearchBar(a.width-2) + "\n")
	b.WriteString(renderExploreHeader(w) + "\n")
	b.WriteString(RenderDivider(a.width-2) + "\n")

	n := len(em.predictions)
	if n == 0 {
		b.WriteString("  No models match your search/filters.\n")
		return b.String()
	}

	vis := emVisibleRows(a)
	end := em.scrollOff + vis
	if end > n {
		end = n
	}

	if em.scrollOff > 0 {
		fmt.Fprintf(&b, "  \u2191 %d above\n", em.scrollOff)
	} else {
		b.WriteString("\n")
	}

	for i := em.scrollOff; i < end; i++ {
		selected := em.cursor == i
		b.WriteString(renderExploreRow(em.predictions[i], em.gpu, w, selected, a.width) + "\n")
	}

	remaining := n - end
	if remaining > 0 {
		fmt.Fprintf(&b, "  \u2193 %d more\n", remaining)
	} else {
		b.WriteString("\n")
	}

	return b.String()
}

func (em *exploreModel) exploreSearchBar(innerWidth int) string {
	query := em.searchQuery
	if query == "" {
		if em.searching {
			query = "type to search... \u2588"
		} else {
			query = "Press / to search..."
		}
	} else if em.searching {
		query += "\u2588"
	}

	searchBox := renderMiniBox("Search [/]", query, 38,
		em.searching || em.searchQuery != "")

	sortLabel := sortFieldLabels[em.sortF]
	if !em.sortAsc {
		sortLabel += "\u2193"
	} else {
		sortLabel += "\u2191"
	}
	sortBox := renderMiniBox("Sort [s]", sortLabel, 24,
		em.sortF != sortByName || !em.sortAsc)

	typeLabel := "All"
	if em.typeFilter != nil {
		typeLabel = string(*em.typeFilter)
	}
	typeBox := renderMiniBox("Type [t]", typeLabel, 20, em.typeFilter != nil)

	fitBox := renderMiniBox("Fit [f]", fitFilterLabel(em.fit), 18, em.fit != fitAll)
	countBox := renderMiniBox("Shown", fmt.Sprintf("%d/%d", len(em.predictions), len(em.allPredictions)), 18, false)

	line := searchBox + " " + sortBox + " " + typeBox + " " + fitBox + " " + countBox
	if lipgloss.Width(line) < innerWidth {
		line += repeat(" ", innerWidth-lipgloss.Width(line))
	}
	return line
}

func (em *exploreModel) explorePreview() string {
	if len(em.predictions) == 0 {
		return ""
	}
	selected := em.predictions[em.cursor]
	return PreviewBar.Render(fmt.Sprintf("  \u25b6 %s  %s",
		truncate(selected.Model.Name, 50),
		selected.Model.ParameterCount))
}

func (em *exploreModel) exploreFooter() string {
	if em.searching {
		return Footer.Render("  Type to search  Enter Apply  Esc Clear")
	}
	return Footer.Render("  \u2191\u2193 Navigate  / Search  F Fit  s Sort  A Asc  D Desc  T Type  Enter Details  B Benchmarks  Esc Back")
}

func emVisibleRows(a *App) int {
	n := a.height - 15
	if n < 1 {
		return 1
	}
	return n
}

func cycleTypeFilter(cur *model.Category) *model.Category {
	cats := model.AllCategories()
	if cur == nil {
		c := cats[0]
		return &c
	}
	for i, c := range cats {
		if c == *cur {
			if i+1 < len(cats) {
				next := cats[i+1]
				return &next
			}
			return nil
		}
	}
	return nil
}

func hasTypeCategory(m *model.Model, want model.Category) bool {
	for _, c := range m.Categories {
		if c == want {
			return true
		}
	}
	return false
}

type exploreCols struct {
	model  int
	params int
	tokS   int
	quant  int
	disk   int
	mode   int
	mem    int
	ctx    int
	date   int
	fit    int
}

func exploreColWidths(termWidth int) exploreCols {
	innerWidth := termWidth - 2
	dot := 4
	params, toks, quant, disk, mode, mem, ctx, date, fit := 10, 7, 10, 11, 6, 6, 6, 8, 8
	fixedOther := params + toks + quant + disk + mode + mem + ctx + date + fit

	modelW := innerWidth - dot - fixedOther
	if modelW < 15 {
		modelW = 15
	}
	return exploreCols{modelW, params, toks, quant, disk, mode, mem, ctx, date, fit}
}

func renderExploreHeader(w exploreCols) string {
	return TableHeader.Render(
		padCell("  \u25cf", 4) +
			padCell("Model", w.model) +
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

func renderExploreRow(p predictor.ModelPrediction, gpu *hardware.GPU, w exploreCols, selected bool, termWidth int) string {
	speed := fmt.Sprintf("%.0f", p.EstTokPerSec)
	if p.FitStatus == predictor.Advanced {
		speed = "~" + fmt.Sprintf("%.0f", p.EstTokPerSec)
	}

	memPct := formatMemPctRaw(p.Model, gpu)
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

	dotText := "  " + dot
	if selected {
		dotText = "\u25b6 " + dot
	}

	displayName := p.Model.Name
	if p.Model.Provider != "" && !strings.HasPrefix(p.Model.Name, p.Model.Provider+"/") {
		displayName = p.Model.Provider + "/" + p.Model.Name
	}

	cells := []struct {
		text  string
		width int
	}{
		{dotText, 4},
		{truncate(displayName, w.model), w.model},
		{truncate(p.Model.ParameterCount, w.params), w.params},
		{truncate(speed, w.tokS), w.tokS},
		{truncate(p.Model.Quantization, w.quant), w.quant},
		{truncate(formatDiskSize(p.Model), w.disk), w.disk},
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

func formatDiskSize(m *model.Model) string {
	weightGB := predictor.ModelWeightSizeGB(m)
	if weightGB <= 0 {
		return "\u2014"
	}
	if weightGB < 1.0 {
		return fmt.Sprintf("%.0fMB", weightGB*1024)
	}
	return fmt.Sprintf("%.1fG", weightGB)
}
