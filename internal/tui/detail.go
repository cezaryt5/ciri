package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"
)

type detailMode int

const (
	detailOverview detailMode = iota
	detailSources
)

type detailQuant struct {
	Quant     model.Quant
	Pred      predictor.ModelPrediction
	SourceIdx int
}

type detailModel struct {
	selected predictor.ModelPrediction
	specs    hardware.Specs
	gpu      *hardware.GPU

	quants       []detailQuant
	recommended  int
	cursor       int
	scroll       int
	mode         detailMode
	sourceCur    int
	sourceScroll int
}

func newDetailModel(selected predictor.ModelPrediction, pred *predictor.Predictor, specs hardware.Specs, gpu *hardware.GPU) *detailModel {
	d := &detailModel{selected: selected, specs: specs, gpu: gpu}
	d.buildQuants(pred)
	return d
}

func (d *detailModel) buildQuants(pred *predictor.Predictor) {
	m := d.selected.Model
	if m == nil {
		return
	}
	if len(m.Quants) == 0 {
		// Legacy flat catalog: synthesize one quant row from the flat fields.
		wb := int64(predictor.ModelWeightSizeGB(m) * 1e9)
		q := model.Quant{
			Label:        m.Quantization,
			WeightsBytes: wb,
			Sources: []model.QuantSource{{
				Repo:          m.Name,
				Publisher:     m.Provider,
				Preferred:     true,
				WeightsBytes:  wb,
				DownloadBytes: wb,
			}},
		}
		d.quants = []detailQuant{{Quant: q, Pred: d.selected}}
		d.recommended = 1
		return
	}

	for _, q := range m.Quants {
		src := q.PreferredSource()
		p := pred.PredictQuant(m, q, src)
		d.quants = append(d.quants, detailQuant{Quant: q, Pred: p})
	}
	sort.SliceStable(d.quants, func(i, j int) bool {
		ri, rj := d.quants[i].Pred.FitStatus == predictor.Recommended,
			d.quants[j].Pred.FitStatus == predictor.Recommended
		if ri != rj {
			return ri
		}
		if d.quants[i].Pred.FitStatus != d.quants[j].Pred.FitStatus {
			return d.quants[i].Pred.FitStatus < d.quants[j].Pred.FitStatus
		}
		return d.quants[i].Quant.Bits > d.quants[j].Quant.Bits
	})
	for i := range d.quants {
		if d.quants[i].Pred.FitStatus == predictor.Recommended {
			d.recommended = i + 1
		}
	}
	if d.recommended == 0 {
		// Nothing fits VRAM; call the best RAM-fit quants recommended.
		for i := range d.quants {
			if d.quants[i].Pred.FitStatus == predictor.Advanced {
				d.recommended = i + 1
			}
		}
	}
	for i := range d.quants {
		if strings.EqualFold(d.quants[i].Quant.Label, m.Quantization) {
			d.cursor = i
			break
		}
	}
}

func (d *detailModel) current() *detailQuant {
	if len(d.quants) == 0 {
		return nil
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
	if d.cursor >= len(d.quants) {
		d.cursor = len(d.quants) - 1
	}
	return &d.quants[d.cursor]
}

func (d *detailModel) currentSource() *model.QuantSource {
	q := d.current()
	if q == nil || len(q.Quant.Sources) == 0 {
		return nil
	}
	idx := q.SourceIdx
	if idx < 0 || idx >= len(q.Quant.Sources) {
		idx = 0
	}
	return &q.Quant.Sources[idx]
}

func (d *detailModel) detailUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	if d.mode == detailSources {
		q := d.current()
		if q == nil {
			d.mode = detailOverview
			return nil
		}
		switch msg.String() {
		case "esc":
			d.mode = detailOverview
		case "up", "k":
			if d.sourceCur > 0 {
				d.sourceCur--
			}
		case "down", "j":
			if d.sourceCur < len(q.Quant.Sources)-1 {
				d.sourceCur++
			}
		case "enter":
			if d.sourceCur >= 0 && d.sourceCur < len(q.Quant.Sources) {
				return d.startDownload(a, &q.Quant.Sources[d.sourceCur])
			}
		}
		return nil
	}

	switch msg.String() {
	case "esc":
		return func() tea.Msg { return navigateMsg{target: screenExplore} }
	case "b":
		return func() tea.Msg { return navigateMsg{target: screenBenchmarks} }
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor < len(d.quants)-1 {
			d.cursor++
		}
	case "left", "h", "s":
		if q := d.current(); q != nil && len(q.Quant.Sources) > 0 {
			q.SourceIdx = (q.SourceIdx - 1 + len(q.Quant.Sources)) % len(q.Quant.Sources)
		}
	case "right", "l":
		if q := d.current(); q != nil && len(q.Quant.Sources) > 0 {
			q.SourceIdx = (q.SourceIdx + 1) % len(q.Quant.Sources)
		}
	case "enter":
		if q := d.current(); q != nil {
			if len(q.Quant.Sources) > 1 {
				d.mode = detailSources
				d.sourceCur = q.SourceIdx
				d.sourceScroll = 0
				return nil
			}
			if src := d.currentSource(); src != nil {
				return d.startDownload(a, src)
			}
		}
	case "d":
		if src := d.currentSource(); src != nil {
			return d.startDownload(a, src)
		}
	}
	return nil
}

func (d *detailModel) startDownload(a *App, src *model.QuantSource) tea.Cmd {
	if a.downloads == nil || src == nil {
		return nil
	}
	repo := strings.TrimSpace(src.Repo)
	if !strings.Contains(repo, "/") {
		return nil
	}
	q := d.current()
	if q == nil {
		return nil
	}
	display := d.selected.Model.Display
	if display == "" {
		display = d.selected.Model.Name
	}
	a.downloads.enqueue(repo, display, q.Quant.Label, src.Files)
	return func() tea.Msg { return navigateMsg{target: screenDownload} }
}

func (d *detailModel) detailView(a *App) string {
	m := d.selected.Model
	inner := a.width - 4
	if inner < 22 {
		inner = 22
	}
	if m == nil {
		return "\n\n  " + Label.Render("NO MODEL SELECTED") + "\n"
	}
	if len(d.quants) == 0 && a.pred != nil {
		d.buildQuants(a.pred)
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + Heading.Render(truncate(m.Display, inner-4)) + "\n")

	provider := strings.TrimSpace(m.Provider)
	if provider == "" {
		provider = "UNKNOWN"
	}
	meta := strings.ToUpper(provider)
	meta += "  /  " + strings.ToUpper(m.Type)
	if m.Density != "" {
		meta += "  /  " + strings.ToUpper(m.Density)
	}
	if m.Format != "" {
		meta += "  /  " + strings.ToUpper(m.Format)
	}
	b.WriteString("  " + Tagline.Render(meta) + "\n")

	b.WriteString(RenderSection("Model", inner) + "\n")
	b.WriteString("  " +
		field("Params", m.ParameterCount, 10) + "  " +
		field("Context", formatContext(m.ContextLength), 9) + "  " +
		field("Arch", truncate(m.Architecture, 18), 9) + "\n")
	if m.IsMoE {
		b.WriteString("  " +
			field("Experts", fmt.Sprintf("%d", m.Experts), 10) + "  " +
			field("Active", fmt.Sprintf("%d", m.ExpertsActive), 9) + "  " +
			field("Quants", fmt.Sprintf("%d", len(d.quants)), 9) + "\n")
	} else {
		b.WriteString("  " +
			field("Quants", fmt.Sprintf("%d", len(d.quants)), 10) + "  " +
			field("License", truncate(m.License, 18), 9) + "  " +
			field("Pipeline", truncate(m.PipelineTag, 18), 9) + "\n")
	}
	b.WriteString("  " +
		field("Downloads", formatNum(m.HfDownloads), 10) + "  " +
		field("Likes", formatNum(m.HfLikes), 9) + "\n")

	if d.mode == detailSources {
		return b.String() + d.sourcesView(a, inner)
	}

	if len(d.quants) == 0 {
		b.WriteString("\n  " + Label.Render("NO QUANTIZED FILES AVAILABLE") + "\n")
		return b.String()
	}

	q := d.current()
	if q != nil {
		b.WriteString(RenderSection("Selected quant", inner) + "\n")
		src := d.currentSource()
		b.WriteString("  " + fitStatusTag(q.Pred.FitStatus) + "  " +
			Value.Render(q.Quant.Label) + "  " +
			Tagline.Render(formatBytes(sourceBytes(src, &q.Quant))) + "  " +
			Tagline.Render(fmt.Sprintf("%.0f tok/s", q.Pred.EstTokPerSec)) + "\n")
	}

	visible := a.height - 20
	if visible < 3 {
		visible = 3
	}
	if d.cursor < d.scroll {
		d.scroll = d.cursor
	}
	if d.cursor >= d.scroll+visible {
		d.scroll = d.cursor - visible + 1
	}
	end := d.scroll + visible
	if end > len(d.quants) {
		end = len(d.quants)
	}

	if d.scroll == 0 {
		label := "No quant fits this machine"
		if d.recommended > 0 {
			if d.quants[0].Pred.FitStatus == predictor.Recommended {
				label = "Recommended for your GPU"
			} else {
				label = "Best available (spills to RAM)"
			}
		}
		b.WriteString("\n" + RenderSection(label, inner) + "\n")
	}
	for i := d.scroll; i < end; i++ {
		if i == d.recommended && d.recommended > 0 {
			b.WriteString("\n" + RenderSection("Other quants", inner) + "\n")
		}
		b.WriteString(d.quantRow(i, d.cursor == i, inner))
	}
	if end < len(d.quants) {
		b.WriteString("\n  " + Tagline.Render(fmt.Sprintf("\u2193 %d MORE", len(d.quants)-end)) + "\n")
	}
	return b.String()
}

func (d *detailModel) quantRow(i int, selected bool, inner int) string {
	q := d.quants[i]
	src := q.Quant.PreferredSource()
	if q.SourceIdx >= 0 && q.SourceIdx < len(q.Quant.Sources) {
		src = &q.Quant.Sources[q.SourceIdx]
	}
	repo := ""
	if src != nil {
		repo = src.Repo
	}
	marker := Faint.Render(" ")
	if selected {
		marker = Cursor.Render("\u258c")
	}
	fit := fitStatusTag(q.Pred.FitStatus)
	label := padRight(q.Quant.Label, 9)
	size := padRight(formatBytes(sourceBytes(src, &q.Quant)), 9)
	shards := fmt.Sprintf("%d file(s)", q.Quant.FileCount)
	if q.Quant.FileCount == 1 {
		shards = "1 file"
	}
	repoW := inner - 61
	if repoW < 8 {
		repoW = 8
	}
	prefix := "  " + marker + " "
	if selected {
		plain := label + "  " + size + "  " + "\u25a0 " + fitLabelPlain(q.Pred.FitStatus) +
			"  " + shards + "  " + truncate(repo, repoW)
		if src != nil && len(q.Quant.Sources) > 1 {
			plain += fmt.Sprintf("  [%d]", len(q.Quant.Sources))
		}
		restW := inner - lipgloss.Width(prefix)
		if restW < 0 {
			restW = 0
		}
		return prefix + SelectedRow.Width(restW).MaxWidth(restW).Render(plain) + "\n"
	}
	row := prefix + Value.Render(label) + "  " + Tagline.Render(size) +
		"  " + fit + "  " + Tagline.Render(shards) + "  " + Tagline.Render(truncate(repo, repoW))
	if src != nil && len(q.Quant.Sources) > 1 {
		row += Tagline.Render(fmt.Sprintf("  [%d]", len(q.Quant.Sources)))
	}
	return truncate(row, inner) + "\n"
}

func (d *detailModel) sourcesView(a *App, inner int) string {
	q := d.current()
	if q == nil || len(q.Quant.Sources) == 0 {
		return "\n  " + Label.Render("NO SOURCES") + "\n"
	}
	visible := a.height - 20
	if visible < 3 {
		visible = 3
	}
	if d.sourceCur < d.sourceScroll {
		d.sourceScroll = d.sourceCur
	}
	if d.sourceCur >= d.sourceScroll+visible {
		d.sourceScroll = d.sourceCur - visible + 1
	}
	end := d.sourceScroll + visible
	if end > len(q.Quant.Sources) {
		end = len(q.Quant.Sources)
	}

	var b strings.Builder
	b.WriteString("\n" + RenderSection(fmt.Sprintf("Sources for %s", q.Quant.Label), inner) + "\n")
	for i := d.sourceScroll; i < end; i++ {
		src := q.Quant.Sources[i]
		marker := Faint.Render(" ")
		if i == d.sourceCur {
			marker = Cursor.Render("\u258c")
		}
		pref := " "
		if src.Preferred {
			pref = "*"
		}
		prefix := "  " + marker + " " + pref + " "
		if i == d.sourceCur {
			plain := truncate(src.Repo, inner-40) + "  " +
				formatBytes(sourceBytes(&src, &q.Quant)) + "  " + formatNum(src.Downloads)
			restW := inner - lipgloss.Width(prefix)
			if restW < 0 {
				restW = 0
			}
			b.WriteString(prefix + SelectedRow.Width(restW).MaxWidth(restW).Render(plain) + "\n")
			continue
		}
		row := prefix + Value.Render(truncate(src.Repo, inner-40)) +
			"  " + Tagline.Render(formatBytes(sourceBytes(&src, &q.Quant))) +
			"  " + Tagline.Render(formatNum(src.Downloads))
		b.WriteString(truncate(row, inner) + "\n")
	}
	if end < len(q.Quant.Sources) {
		b.WriteString("  " + Tagline.Render(fmt.Sprintf("\u2193 %d MORE", len(q.Quant.Sources)-end)) + "\n")
	}
	b.WriteString("\n  " + Tagline.Render("ENTER DOWNLOAD   ESC BACK   ↑↓ SOURCE") + "\n")
	return b.String()
}

func sourceBytes(src *model.QuantSource, q *model.Quant) int64 {
	if src != nil && src.WeightsBytes > 0 {
		return src.WeightsBytes
	}
	return q.WeightsBytes
}

func formatContext(n int) string {
	if n <= 0 {
		return "\u2014"
	}
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
