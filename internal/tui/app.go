package tui

import (
	"strings"

	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
	"github.com/cezaryt5/Can_I_Run_IT/internal/model"
	"github.com/cezaryt5/Can_I_Run_IT/internal/predictor"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// screen identifies which substate is active.
type screen int

const (
	screenHome screen = iota
	screenResults
	screenDetail
	screenBenchmarks
)

// navigateMsg signals the root model to switch to a different screen.
type navigateMsg struct {
	target screen
}

// App is the root Bubble Tea model — it delegates to the current substate.
type App struct {
	width, height int

	screen      screen
	benchOrigin screen
	home        *homeModel
	results     *resultsModel
	detail      *detailModel
	bench       *benchmarksModel

	specs    hardware.Specs
	gpu      *hardware.GPU
	models   []model.Model
	pred     *predictor.Predictor
	benchDB  *predictor.BenchmarkDB
	category model.Category
	counts   map[model.Category]int
}

// NewApp — internal/tui/app.go:49
// Called from: cmd/ciri/main.go:60
// Creates the root Bubble Tea App model. Initialises the home screen and
// pre-computes category counts from the Predictor. The home screen is shown
// first (screenHome).
func NewApp(specs hardware.Specs, gpu *hardware.GPU, models []model.Model, pred *predictor.Predictor, benchDB *predictor.BenchmarkDB) *App {
	counts := pred.CountByCategory()
	return &App{
		screen:  screenHome,
		home:    &homeModel{},
		specs:   specs,
		gpu:     gpu,
		models:  models,
		pred:    pred,
		benchDB: benchDB,
		counts:  counts,
	}
}

// Init — internal/tui/app.go:63
// Called from: Bubble Tea runtime (tea.NewProgram)
// Bubble Tea lifecycle method. Returns nil — no initial command needed.
func (a *App) Init() tea.Cmd {
	return nil
}

// Update — internal/tui/app.go:67
// Called from: Bubble Tea runtime
// Routes messages to the active screen's update handler. Handles global
// keybindings (ctrl+q, q to quit), window resize events, and navigation
// messages (navigateMsg) that switch between screens.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		return a, nil

	case navigateMsg:
		// Remember where a benchmarks view was opened from so Esc returns there.
		if msg.target == screenBenchmarks {
			a.benchOrigin = a.screen
		}
		a.screen = msg.target
		// We only instantiate if it requires fresh data passing not handled elsewhere.
		// screenDetail is purposely omitted here because results.go sets it up directly.
		if a.screen == screenBenchmarks && a.detail != nil {
			a.bench = newBenchmarksModel(a.detail.selected, a.gpu, a.benchDB, a.specs)
		}
		return a, nil

	case tea.KeyMsg:
		// Global quit works on every page. ctrl+c always quits; "q" quits
		// unless the user is typing into a text input (e.g. results search).
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		if msg.String() == "q" && !a.isTextInput() {
			return a, tea.Quit
		}
		if a.screen == screenHome {
			return a, a.home.homeUpdate(a, msg)
		}
		if a.screen == screenResults && a.results != nil {
			return a, a.results.resultsUpdate(a, msg)
		}
		if a.screen == screenDetail && a.detail != nil {
			return a, a.detail.detailUpdate(a, msg)
		}
		if a.screen == screenBenchmarks && a.bench != nil {
			return a, a.bench.benchUpdate(a, msg)
		}
	}

	return a, nil
}

// View — internal/tui/app.go:113
// Called from: Bubble Tea runtime
// Renders the complete UI: hardware bar at the top inside a titled box,
// then the active screen's content (home, results, detail, or benchmarks),
// plus a preview line and footer for the results screen.
func (a *App) View() string {
	var sb strings.Builder

	headerContent := RenderHardwareBar(a.specs, a.gpu, a.width) + "\n" + renderToolAvail(a.specs)
	sb.WriteString(RenderBox("CIRI", headerContent, a.width) + "\n")

	switch a.screen {
	case screenResults:
		if a.results == nil {
			a.results = newResultsModel(a.pred, a.category, a.specs, a.gpu)
		}
		sb.WriteString(RenderBox(a.label(), a.results.resultsView(a), a.width) + "\n")
		if len(a.results.predictions) > 0 {
			sb.WriteString(a.results.resultsPreview() + "\n")
		}
		sb.WriteString(a.results.resultsFooter())
	case screenHome:
		sb.WriteString(RenderBox(a.label(), a.home.homeView(a), a.width))
	case screenDetail:
		if a.detail == nil {
			a.detail = newDetailModel(a.pred, a.category, a.specs, a.gpu)
		}
		sb.WriteString(RenderBox(a.label(), a.detail.detailView(a), a.width))
	case screenBenchmarks:
		if a.bench == nil && a.detail != nil {
			a.bench = newBenchmarksModel(a.detail.selected, a.gpu, a.benchDB, a.specs)
		}
		if a.bench != nil {
			sb.WriteString(RenderBox(a.label(), a.bench.benchView(a), a.width))
		}
	}

	contentHeight := a.height - 5
	if contentHeight < 1 {
		contentHeight = 1
	}
	return lipgloss.NewStyle().Width(a.width).Height(contentHeight).Render(sb.String())
}

// isTextInput — internal/tui/app.go:154
// Called from: app.go:93 (in Update)
// Reports whether the active screen is capturing free text input. When true,
// global single-key shortcuts (e.g. "q" for quit) are suppressed so the
// input field receives them instead.
func (a *App) isTextInput() bool {
	return a.screen == screenResults && a.results != nil && a.results.searching
}

// label — internal/tui/app.go:158
// Called from: app.go:124,130,135,141 (in View)
// Returns the title string for the currently active screen's box. For
// detail and benchmarks screens, includes the model name (truncated).
func (a *App) label() string {
	switch a.screen {
	case screenHome:
		return "Home"
	case screenResults:
		return string(a.category)
	case screenDetail:
		if a.detail != nil {
			return truncate(a.detail.selected.Model.Name, 40)
		}
		return "Detail"
	case screenBenchmarks:
		if a.bench != nil {
			return "Benchmarks: " + truncate(a.bench.selected.Model.Name, 30)
		}
		return "Benchmarks"
	}
	return ""
}

// renderToolAvail — internal/tui/app.go:178
// Called from: app.go:116 (in View)
// Renders a compact line showing checkmarks (✓) or crosses (✗) for Ollama
// and llama.cpp availability.
func renderToolAvail(specs hardware.Specs) string {
	var parts []string
	if specs.HasOllama {
		parts = append(parts, "Ollama: \u2713")
	} else {
		parts = append(parts, "Ollama: \u00d7")
	}
	if specs.HasLlamaCPP {
		parts = append(parts, "llama.cpp: \u2713")
	} else {
		parts = append(parts, "llama.cpp: \u00d7")
	}
	return " " + strings.Join(parts, " \u2502 ") + " "
}
