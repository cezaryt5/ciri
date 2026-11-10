package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cezaryt5/ciri/internal/hardware"
)

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestExplore_TapSTogglesDirection(t *testing.T) {
	app := testApp("")
	em := newExploreModel(app.pred, app.specs, app.gpu)

	start := em.sortAsc
	em.exploreUpdate(app, runeKey('S'))
	if em.sortAsc == start {
		t.Fatalf("single S press must toggle the sort direction")
	}
	if em.sortMode {
		t.Fatalf("single S press must not open the arrow sort mode")
	}
}

func TestExplore_RapidSReopensSortMode(t *testing.T) {
	app := testApp("")
	em := newExploreModel(app.pred, app.specs, app.gpu)

	em.exploreUpdate(app, runeKey('S'))
	em.exploreUpdate(app, runeKey('S'))
	if !em.sortMode {
		t.Fatalf("a second S press within %s must open the arrow sort mode", sortModeWindow)
	}
}

func TestExplore_StaleSDoesNotOpenSortMode(t *testing.T) {
	app := testApp("")
	em := newExploreModel(app.pred, app.specs, app.gpu)

	em.exploreUpdate(app, runeKey('S'))
	em.lastSKey = time.Now().Add(-time.Second)
	em.exploreUpdate(app, runeKey('S'))
	if em.sortMode {
		t.Fatalf("S presses further apart than %s must not open sort mode", sortModeWindow)
	}
}

func TestExplore_SortModeArrows(t *testing.T) {
	app := testApp("")
	em := newExploreModel(app.pred, app.specs, app.gpu)
	em.sortMode = true
	em.sortF = sortByName
	em.sortAsc = true

	em.exploreUpdate(app, tea.KeyMsg{Type: tea.KeyDown})
	if em.sortAsc {
		t.Fatalf("down must select descending order")
	}
	em.exploreUpdate(app, tea.KeyMsg{Type: tea.KeyUp})
	if !em.sortAsc {
		t.Fatalf("up must select ascending order")
	}
	em.exploreUpdate(app, tea.KeyMsg{Type: tea.KeyRight})
	if em.sortF != sortByParams {
		t.Fatalf("right must cycle to the next sort column, got %v", em.sortF)
	}
	em.exploreUpdate(app, tea.KeyMsg{Type: tea.KeyLeft})
	if em.sortF != sortByName {
		t.Fatalf("left must cycle back to the previous sort column, got %v", em.sortF)
	}
	if cmd := em.exploreUpdate(app, tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		t.Fatalf("esc inside sort mode must only close the mode, not navigate")
	}
	if em.sortMode {
		t.Fatalf("esc must close the arrow sort mode")
	}
}

func TestPaintCanvasPadsEveryLine(t *testing.T) {
	const width = 20
	out := paintCanvas("short\nlonger line of text that overflows", width)
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w != width {
			t.Fatalf("line %d is %d cells wide, want %d: %q", i, w, width, line)
		}
	}
}

func TestPadLineTruncatesWithoutWrapping(t *testing.T) {
	const width = 10
	out := padLine("longer line of text that overflows", width)
	if strings.Contains(out, "\n") {
		t.Fatalf("padLine wrapped instead of truncating: %q", out)
	}
	if w := ansi.StringWidth(out); w != width {
		t.Fatalf("padLine width = %d, want %d: %q", w, width, out)
	}
}

func TestPaintCanvasPadsHeight(t *testing.T) {
	const width, height = 20, 6
	out := paintCanvasHeight("short\nlonger line of text that overflows", width, height)
	lines := strings.Split(out, "\n")
	if len(lines) != height {
		t.Fatalf("got %d lines, want %d", len(lines), height)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w != width {
			t.Fatalf("line %d is %d cells wide, want %d: %q", i, w, width, line)
		}
	}
}

func TestApp_ViewShowsLiveMetrics(t *testing.T) {
	app := testApp("1.0.0")
	app.screen = screenExplore
	app.explore = newExploreModel(app.pred, app.specs, app.gpu)
	app.metrics = hardware.Metrics{
		CPUPercent: 12, CPUPercentKnown: true, CPUTempC: 54,
		MemTotalGB: 64, MemUsedGB: 16,
		GPUUtilPct: 47, GPUUtilKnown: true, GPUTempC: 61,
	}
	view := app.View()
	for _, want := range []string{"47%", "61°C", "12%", "54°C"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view must show live metric %q, got:\n%s", want, view)
		}
	}
}
