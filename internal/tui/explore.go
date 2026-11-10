package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"

	tea "github.com/charmbracelet/bubbletea"
)

// sortModeWindow is how quickly a second S press must follow the first to
// count as "hold S" and open the arrow-driven sort mode. A single tap keeps
// the default behavior (toggle ascending/descending).
const sortModeWindow = 500 * time.Millisecond

type sortField int

const (
	sortByName sortField = iota
	sortByParams
	sortBySpeed
	sortByDisk
	sortByFit
	sortFieldCount
)

var sortFieldLabels = [sortFieldCount]string{
	"NAME", "PARAMS", "SPEED", "DISK", "FIT",
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

	searching     bool
	searchQuery   string
	fit           fitFilter
	sortF         sortField
	sortAsc       bool
	typeFilter    string
	densityFilter string

	// sortMode is the arrow-driven sort editor opened by pressing S twice in
	// quick succession. lastSKey stamps the previous S press for that check.
	sortMode bool
	lastSKey time.Time
}

func newExploreModel(pred *predictor.Predictor, specs hardware.Specs, gpu *hardware.GPU) *exploreModel {
	em := &exploreModel{
		allPredictions: pred.PredictAll(),
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
		if em.typeFilter != "" && !strings.EqualFold(p.Model.Type, em.typeFilter) {
			continue
		}
		if em.densityFilter != "" && !strings.EqualFold(p.Model.Density, em.densityFilter) {
			continue
		}
		if q != "" {
			hay := strings.ToLower(p.Model.Display + " " + p.Model.Name + " " +
				p.Model.Provider + " " + p.Model.Type + " " + p.Model.Density + " " +
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
	case sortByFit:
		sort.SliceStable(filtered, func(i, j int) bool {
			if em.sortAsc {
				return filtered[i].FitStatus < filtered[j].FitStatus
			}
			return filtered[i].FitStatus > filtered[j].FitStatus
		})
	case sortByName:
		sort.SliceStable(filtered, func(i, j int) bool {
			ai, aj := filtered[i].Model.Display, filtered[j].Model.Display
			if ai == "" {
				ai = filtered[i].Model.Name
			}
			if aj == "" {
				aj = filtered[j].Model.Name
			}
			if em.sortAsc {
				return strings.ToLower(ai) < strings.ToLower(aj)
			}
			return strings.ToLower(ai) > strings.ToLower(aj)
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

	if em.sortMode {
		return em.sortModeUpdate(msg)
	}

	switch msg.String() {
	case "esc":
		if em.searchQuery != "" || em.fit != fitAll || em.sortF != sortByName || !em.sortAsc ||
			em.typeFilter != "" || em.densityFilter != "" {
			em.searchQuery = ""
			em.fit = fitAll
			em.sortF = sortByName
			em.sortAsc = true
			em.typeFilter = ""
			em.densityFilter = ""
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
	case "S":
		// Tap: flip ascending/descending (the default way). Press again
		// quickly: open the arrow-driven sort mode so the user can adjust
		// field and direction without cycling blindly.
		now := time.Now()
		if now.Sub(em.lastSKey) <= sortModeWindow {
			em.sortMode = true
		}
		em.lastSKey = now
		em.sortAsc = !em.sortAsc
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
	case "m":
		em.densityFilter = cycleDensityFilter(em.densityFilter)
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
			a.detail = newDetailModel(em.predictions[em.cursor], a.pred, em.specs, em.gpu)
			return func() tea.Msg { return navigateMsg{target: screenDetail} }
		}
	case "b":
		if em.cursor >= 0 && em.cursor < n {
			a.detail = newDetailModel(em.predictions[em.cursor], a.pred, em.specs, em.gpu)
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

// sortModeUpdate handles the arrow-driven sort editor. Up = ascending,
// Down = descending, Left/Right cycle the sort column. Any other key leaves
// the mode (the key is swallowed, so Esc exits the editor before it leaves
// the screen).
func (em *exploreModel) sortModeUpdate(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		em.sortAsc = true
	case "down", "j":
		em.sortAsc = false
	case "left", "h":
		em.sortF = (em.sortF + sortFieldCount - 1) % sortFieldCount
	case "right", "l":
		em.sortF = (em.sortF + 1) % sortFieldCount
	default:
		em.sortMode = false
		return nil
	}
	em.applyFilters()
	return nil
}

func (em *exploreModel) exploreView(a *App) string {
	inner := a.width - 2
	contentW := inner - 4
	if inner < 28 {
		inner = 28
		contentW = 24
	}

	var b strings.Builder

	b.WriteString("  " + em.exploreControls(contentW) + "\n")
	if em.searching {
		b.WriteString("  " + StatusOn.Render(strings.Repeat("\u2500", contentW)) + "\n")
	} else {
		b.WriteString("  " + Rule.Render(strings.Repeat("\u2500", contentW)) + "\n")
	}

	w := exploreColWidths(a.width)
	b.WriteString(renderExploreHeader(w) + "\n")
	b.WriteString("  " + RenderRule(contentW) + "\n")

	n := len(em.predictions)
	if n == 0 {
		b.WriteString("  " + Label.Render("NO MODELS MATCH THE CURRENT FILTERS") + "\n")
		b.WriteString("  " + Tagline.Render("ADJUST THE SEARCH OR PRESS ESC TO RESET") + "\n")
		b.WriteString("\n")
		return b.String()
	}

	vis := emVisibleRows(a)
	end := em.scrollOff + vis
	if end > n {
		end = n
	}

	if em.scrollOff > 0 {
		b.WriteString("  " + Tagline.Render(fmt.Sprintf("\u2191 %d ABOVE", em.scrollOff)) + "\n")
	} else {
		b.WriteString("\n")
	}

	for i := em.scrollOff; i < end; i++ {
		b.WriteString(renderExploreRow(em.predictions[i], em.gpu, w, em.cursor == i, a.width) + "\n")
	}

	if remaining := n - end; remaining > 0 {
		b.WriteString("  " + Tagline.Render(fmt.Sprintf("\u2193 %d MORE", remaining)) + "\n")
	} else {
		b.WriteString("\n")
	}

	return b.String()
}

// exploreControls combines the input (bottom border turns white on focus)
// with the filter chips and result count.
func (em *exploreModel) exploreControls(inner int) string {
	placeholder := "press /"
	if em.searching {
		placeholder = "type..."
	}

	prefixPlain := "/ SEARCH  "
	cursor := ""
	cursorW := 0
	if em.searching {
		cursor = "\u2588"
		cursorW = 1
	}

	rawInput := em.searchQuery
	isPlaceholder := rawInput == ""
	if isPlaceholder {
		rawInput = placeholder
	}

	sortDir := "\u2191"
	if !em.sortAsc {
		sortDir = "\u2193"
	}
	sortVal := sortFieldLabels[em.sortF] + " " + sortDir
	if em.sortMode {
		sortVal += " MODE"
	}
	sortChip := renderChip("S", "SORT", sortVal, true)
	fitChip := renderChip("F", "FIT", fitFilterLabel(em.fit), em.fit != fitAll)
	typeChip := renderChip("T", "TYPE", typeFilterLabel(em.typeFilter), em.typeFilter != "")
	densityChip := renderChip("M", "DENSITY", densityFilterLabel(em.densityFilter), em.densityFilter != "")
	count := Label.Render("SHOWN") + " " + Value.Render(
		fmt.Sprintf("%d/%d", len(em.predictions), len(em.allPredictions)))

	minSearch := lipgloss.Width(prefixPlain) + 8

	parts := []string{sortChip}
	for _, cand := range []string{fitChip, typeChip, densityChip} {
		try := append(append([]string{}, parts...), cand)
		tryLine := strings.Join(try, "  ") + "  " + count
		if minSearch+2+lipgloss.Width(tryLine) <= inner {
			parts = try
		}
	}
	right := strings.Join(parts, "  ") + "  " + count
	if minSearch+2+lipgloss.Width(right) > inner {
		right = ""
	}

	leftMax := inner
	if right != "" {
		leftMax -= lipgloss.Width(right) + 2
	}
	if leftMax < lipgloss.Width(prefixPlain)+4 {
		right = ""
		leftMax = inner
	}

	textMax := leftMax - lipgloss.Width(prefixPlain) - cursorW
	if textMax < 1 {
		textMax = 1
	}
	input := truncate(rawInput, textMax)

	left := HintKey.Render("/") + " " + Label.Render("SEARCH") + "  "
	if isPlaceholder {
		left += Faint.Render(input)
	} else {
		left += Value.Render(input)
	}
	if em.searching {
		left += StatusOn.Render(cursor)
	}

	if right == "" {
		return left
	}
	return padRight(left, leftMax) + "  " + right
}

func emVisibleRows(a *App) int {
	n := a.height - 13
	if n < 1 {
		return 1
	}
	return n
}

var modelTypeOrder = []string{
	"", "generative", "vlm", "ocr", "asr", "tts", "audio", "diffusion", "embedding", "other",
}

var densityOrder = []string{"", "dense", "moe", "unknown"}

func cycleString(cur string, values []string) string {
	for i, v := range values {
		if v == cur {
			if i+1 < len(values) {
				return values[i+1]
			}
			return values[0]
		}
	}
	return values[0]
}

func cycleTypeFilter(cur string) string {
	return cycleString(cur, modelTypeOrder)
}

func cycleDensityFilter(cur string) string {
	return cycleString(cur, densityOrder)
}

func typeFilterLabel(c string) string {
	if c == "" {
		return "ALL"
	}
	return strings.ToUpper(c)
}

func densityFilterLabel(c string) string {
	if c == "" {
		return "ALL"
	}
	return strings.ToUpper(c)
}

type exploreCols struct {
	model   int
	params  int
	tokS    int
	typ     int
	density int
	quant   int
	disk    int
	mode    int
	mem     int
	ctx     int
	fit     int
}

// exploreColWidths collapses secondary columns as the terminal narrows so
// the table never spills past the frame (the old layout overflowed at 80).
func exploreColWidths(termWidth int) exploreCols {
	inner := termWidth - 6
	if inner < 22 {
		inner = 22
	}

	c := exploreCols{tokS: 6, fit: 11}
	if inner >= 64 {
		c.params = 8
		c.mem = 5
	}
	if inner >= 80 {
		c.mode = 5
	}
	if inner >= 88 {
		c.quant = 9
	}
	if inner >= 96 {
		c.ctx = 5
	}
	if inner >= 112 {
		c.disk = 8
	}
	if inner >= 120 {
		c.typ = 12
	}
	if inner >= 132 {
		c.density = 8
	}

	fixed := 2 + c.tokS + c.fit + c.params + c.mem + c.mode + c.quant + c.ctx + c.disk + c.typ + c.density
	c.model = inner - fixed
	if c.model < 8 {
		c.model = 8
	}
	return c
}

func renderExploreHeader(w exploreCols) string {
	var b strings.Builder

	b.WriteString("  ")
	b.WriteString(renderCell("", 2, lipgloss.Left))
	b.WriteString(renderCell(Label.Render("MODEL"), w.model, lipgloss.Left))
	if w.params > 0 {
		b.WriteString(renderCell(Label.Render("PARAMS"), w.params, lipgloss.Right))
	}
	b.WriteString(renderCell(Label.Render("TOK/S"), w.tokS, lipgloss.Right))
	if w.typ > 0 {
		b.WriteString(renderCell(Label.Render("TYPE"), w.typ, lipgloss.Left))
	}
	if w.density > 0 {
		b.WriteString(renderCell(Label.Render("DENSITY"), w.density, lipgloss.Left))
	}
	if w.quant > 0 {
		b.WriteString(renderCell(Label.Render("QUANT"), w.quant, lipgloss.Left))
	}
	if w.disk > 0 {
		b.WriteString(renderCell(Label.Render("DISK"), w.disk, lipgloss.Right))
	}
	if w.mode > 0 {
		b.WriteString(renderCell(Label.Render("MODE"), w.mode, lipgloss.Left))
	}
	if w.mem > 0 {
		b.WriteString(renderCell(Label.Render("MEM"), w.mem, lipgloss.Right))
	}
	if w.ctx > 0 {
		b.WriteString(renderCell(Label.Render("CTX"), w.ctx, lipgloss.Right))
	}
	b.WriteString(renderCell(Label.Render("FIT"), w.fit, lipgloss.Left))

	return b.String()
}

type exploreCell struct {
	plain  string
	styled string
	width  int
	align  lipgloss.Position
}

func renderExploreRow(p predictor.ModelPrediction, gpu *hardware.GPU, w exploreCols, selected bool, termWidth int) string {
	speed := "\u2014"
	switch p.Model.Type {
	case "generative", "vlm", "ocr":
		speed = fmt.Sprintf("%.0f", p.EstTokPerSec)
		if p.FitStatus == predictor.Advanced {
			speed = "~" + speed
		}
	}

	displayName := p.Model.Display
	if displayName == "" {
		displayName = p.Model.Name
	}
	if p.Model.Provider != "" && !strings.HasPrefix(displayName, p.Model.Provider+"/") {
		displayName = p.Model.Provider + "/" + displayName
	}
	displayName = truncate(displayName, w.model-1)

	params := p.Model.ParameterCount
	if params == "" {
		params = "\u2014"
	}

	memVal := formatMemPctRaw(p.Model, gpu)
	memStyle := MemPctGood
	if memVal != "\u2014" {
		var v float64
		fmt.Sscanf(memVal, "%f%%", &v)
		switch {
		case v < 50:
			memStyle = MemPctGood
		case v <= 80:
			memStyle = MemPctWarn
		default:
			memStyle = MemPctBad
		}
	}

	markerPlain := "  "
	markerStyled := Faint.Render(" ")
	if selected {
		markerPlain = "\u258c "
	}

	cells := []exploreCell{
		{plain: markerPlain, styled: markerStyled, width: 2, align: lipgloss.Left},
		{plain: displayName, styled: Value.Render(displayName), width: w.model, align: lipgloss.Left},
	}
	if w.params > 0 {
		v := truncate(params, w.params-1)
		cells = append(cells, exploreCell{v, ValueDim.Render(v), w.params, lipgloss.Right})
	}
	cells = append(cells, exploreCell{speed, Value.Render(speed), w.tokS, lipgloss.Right})
	if w.typ > 0 {
		v := truncate(strings.ToUpper(p.Model.Type), w.typ-1)
		if v == "" {
			v = "\u2014"
		}
		cells = append(cells, exploreCell{v, Tagline.Render(v), w.typ, lipgloss.Left})
	}
	if w.density > 0 {
		v := truncate(strings.ToUpper(p.Model.Density), w.density-1)
		if v == "" {
			v = "\u2014"
		}
		cells = append(cells, exploreCell{v, Tagline.Render(v), w.density, lipgloss.Left})
	}
	if w.quant > 0 {
		v := truncate(p.Model.Quantization, w.quant-1)
		cells = append(cells, exploreCell{v, Tagline.Render(v), w.quant, lipgloss.Left})
	}
	if w.disk > 0 {
		v := formatDiskSize(p.Model)
		cells = append(cells, exploreCell{v, Tagline.Render(v), w.disk, lipgloss.Right})
	}
	if w.mode > 0 {
		v := formatMode(p.FitStatus)
		cells = append(cells, exploreCell{v, Tagline.Render(v), w.mode, lipgloss.Left})
	}
	if w.mem > 0 {
		cells = append(cells, exploreCell{memVal, memStyle.Render(memVal), w.mem, lipgloss.Right})
	}
	if w.ctx > 0 {
		v := formatContext(p.Model.ContextLength)
		cells = append(cells, exploreCell{v, Tagline.Render(v), w.ctx, lipgloss.Right})
	}
	cells = append(cells, exploreCell{
		plain:  "\u25a0 " + fitLabelPlain(p.FitStatus),
		styled: fitSquare(p.FitStatus) + " " + fitLabel(p.FitStatus),
		width:  w.fit,
		align:  lipgloss.Left,
	})

	if selected {
		var rest strings.Builder
		for _, c := range cells[1:] {
			rest.WriteString(renderCell(c.plain, c.width, c.align))
		}
		prefix := "  " + Cursor.Render("\u258c") + " "
		restW := termWidth - 2 - lipgloss.Width(prefix)
		if restW < 0 {
			restW = 0
		}
		return prefix + SelectedRow.Width(restW).MaxWidth(restW).Render(rest.String())
	}

	var line strings.Builder
	for _, c := range cells {
		line.WriteString(renderCell(c.styled, c.width, c.align))
	}
	return "  " + line.String()
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
