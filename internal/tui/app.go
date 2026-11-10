package tui

import (
	"strings"
	"time"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"

	tea "github.com/charmbracelet/bubbletea"
)

// telemetryInterval is how often hardware load (CPU/GPU/RAM/VRAM/temps) is
// resampled for the chrome strip. Two seconds keeps the gauges alive without
// paying for an nvidia-smi exec more often than needed.
const telemetryInterval = 2 * time.Second

type telemetryTickMsg struct{}

type telemetryMsg struct {
	metrics hardware.Metrics
}

type screen int

const (
	screenHome screen = iota
	screenChat
	screenExplore
	screenDetail
	screenBenchmarks
	screenDownload
	screenLocal
	screenSettings
)

type navigateMsg struct {
	target screen
}

type App struct {
	width, height int

	screen      screen
	benchOrigin screen
	dlOrigin    screen
	home        *homeModel
	chat        *chatModel
	explore     *exploreModel
	detail      *detailModel
	bench       *benchmarksModel
	downloads   *downloadsModel
	local       *localModel
	settings    *settingsModel

	specs   hardware.Specs
	gpu     *hardware.GPU
	models  []model.Model
	pred    *predictor.Predictor
	benchDB *predictor.BenchmarkDB
	version string

	metrics hardware.Metrics
}

func NewApp(specs hardware.Specs, gpu *hardware.GPU, models []model.Model, pred *predictor.Predictor, benchDB *predictor.BenchmarkDB, version string) *App {
	return &App{
		screen:    screenHome,
		home:      newHomeModel(),
		specs:     specs,
		gpu:       gpu,
		models:    models,
		pred:      pred,
		benchDB:   benchDB,
		version:   version,
		downloads: newDownloadsModel(nil),
	}
}

func (a *App) Init() tea.Cmd {
	if a.downloads == nil {
		a.downloads = newDownloadsModel(nil)
	}
	return tea.Batch(telemetryTick(), sampleTelemetry(a.gpu), a.downloads.waitEvent())
}

// telemetryTick schedules the next hardware sample. Each tick re-arms itself
// so the chrome gauges keep refreshing for the life of the program.
func telemetryTick() tea.Cmd {
	return tea.Tick(telemetryInterval, func(time.Time) tea.Msg {
		return telemetryTickMsg{}
	})
}

// sampleTelemetry runs hardware.Sample off the Update goroutine — the NVIDIA
// path shells out to nvidia-smi, which must never block the UI loop.
func sampleTelemetry(gpu *hardware.GPU) tea.Cmd {
	return func() tea.Msg {
		return telemetryMsg{metrics: hardware.Sample(gpu)}
	}
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Download and chat messages are handled on every screen so long-running
	// work keeps running while the user browses the catalog.
	if a.downloads == nil {
		a.downloads = newDownloadsModel(nil)
	}
	if cmd := a.downloads.handleMsg(msg); cmd != nil {
		return a, cmd
	}
	if a.chat != nil {
		if cmd := a.chat.handleMsg(a, msg); cmd != nil {
			return a, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		return a, nil

	case telemetryTickMsg:
		return a, tea.Batch(telemetryTick(), sampleTelemetry(a.gpu))

	case telemetryMsg:
		a.metrics = msg.metrics
		return a, nil

	case navigateMsg:
		var cmd tea.Cmd
		if msg.target == screenBenchmarks {
			a.benchOrigin = a.screen
		}
		if msg.target == screenDownload {
			a.dlOrigin = a.screen
		}
		if msg.target == screenChat && a.chat == nil {
			a.chat = newChatModel(a.gpu)
			cmd = a.chat.initCmd()
		}
		a.screen = msg.target
		if a.screen == screenBenchmarks && a.detail != nil {
			a.bench = newBenchmarksModel(a.detail.selected, a.gpu, a.benchDB, a.specs)
		}
		return a, cmd

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if a.chat != nil {
				a.chat.close()
			}
			return a, tea.Quit
		}
		if msg.String() == "q" && !a.isTextInput() {
			if a.chat != nil {
				a.chat.close()
			}
			return a, tea.Quit
		}
		if a.screen == screenHome {
			return a, a.home.homeUpdate(a, msg)
		}
		if a.screen == screenChat && a.chat != nil {
			return a, a.chat.update(a, msg)
		}
		if a.screen == screenExplore && a.explore != nil {
			return a, a.explore.exploreUpdate(a, msg)
		}
		if a.screen == screenDetail && a.detail != nil {
			return a, a.detail.detailUpdate(a, msg)
		}
		if a.screen == screenBenchmarks && a.bench != nil {
			return a, a.bench.benchUpdate(a, msg)
		}
		if a.screen == screenDownload && a.downloads != nil {
			return a, a.downloads.update(a, msg)
		}
		if a.screen == screenLocal && a.local != nil {
			return a, a.local.localUpdate(a, msg)
		}
		if a.screen == screenSettings && a.settings != nil {
			return a, a.settings.settingsUpdate(a, msg)
		}
	}

	return a, nil
}

func (a *App) View() string {
	width := a.width
	if width <= 0 {
		width = 80
	}

	if a.screen == screenHome {
		return paintCanvasHeight(a.home.homeScreenView(a, width), width, a.height)
	}

	var b strings.Builder
	b.WriteString(renderChrome(a, width))
	b.WriteString("\n")
	b.WriteString(RenderPanel(a.label(), a.screenContent(), width))
	b.WriteString("\n")
	b.WriteString(renderHints(width, a.footerHints()...))
	return paintCanvasHeight(b.String(), width, a.height)
}

func (a *App) screenContent() string {
	switch a.screen {
	case screenChat:
		if a.chat == nil {
			return "\n\n  " + Label.Render("CHAT NOT INITIALIZED") + "\n"
		}
		return a.chat.view(a)
	case screenExplore:
		if a.explore == nil {
			a.explore = newExploreModel(a.pred, a.specs, a.gpu)
		}
		return a.explore.exploreView(a)
	case screenHome:
		return ""
	case screenDetail:
		if a.detail == nil {
			return "\n\n  " + Label.Render("NO MODEL SELECTED") + "\n"
		}
		return a.detail.detailView(a)
	case screenBenchmarks:
		if a.bench == nil && a.detail != nil {
			a.bench = newBenchmarksModel(a.detail.selected, a.gpu, a.benchDB, a.specs)
		}
		if a.bench == nil {
			return "\n\n  " + Label.Render("NO MODEL SELECTED") + "\n"
		}
		return a.bench.benchView(a)
	case screenDownload:
		if a.downloads == nil {
			a.downloads = newDownloadsModel(nil)
		}
		return a.downloads.view(a)
	case screenLocal:
		if a.local == nil {
			a.local = &localModel{}
		}
		return a.local.localView(a)
	case screenSettings:
		if a.settings == nil {
			a.settings = &settingsModel{specs: a.specs, gpu: a.gpu}
		}
		return a.settings.settingsView(a)
	}
	return ""
}

func (a *App) isTextInput() bool {
	if a.screen == screenChat {
		return true
	}
	return a.screen == screenExplore && a.explore != nil && a.explore.searching
}

func (a *App) label() string {
	switch a.screen {
	case screenHome:
		return "Command"
	case screenChat:
		return "Chat"
	case screenExplore:
		return "Explore Models"
	case screenDetail:
		return "Model Detail"
	case screenBenchmarks:
		return "Benchmarks"
	case screenDownload:
		return "Downloads"
	case screenLocal:
		return "Local Models"
	case screenSettings:
		return "Settings / Hardware"
	}
	return ""
}

func (a *App) footerHints() []keyHint {
	switch a.screen {
	case screenHome:
		return []keyHint{
			{"\u2191\u2193", "move"},
			{"enter", "open"},
			{"q", "quit"},
		}
	case screenChat:
		if a.chat != nil && !a.chat.ready {
			return []keyHint{
				{"ctrl+r", "retry"},
				{"esc", "back"},
				{"ctrl+c", "quit"},
			}
		}
		if a.chat != nil && a.chat.running {
			return []keyHint{
				{"esc", "cancel"},
				{"pgup/pgdn", "scroll"},
				{"ctrl+c", "quit"},
			}
		}
		return []keyHint{
			{"enter", "send"},
			{"ctrl+o", "analysis"},
			{"ctrl+n", "new"},
			{"ctrl+s", "export"},
			{"esc", "back"},
			{"ctrl+c", "quit"},
		}
	case screenExplore:
		if a.explore != nil && a.explore.searching {
			return []keyHint{
				{"type", "filter"},
				{"enter", "apply"},
				{"esc", "clear"},
			}
		}
		if a.explore != nil && a.explore.sortMode {
			return []keyHint{
				{"\u2190\u2192", "field"},
				{"\u2191\u2193", "direction"},
				{"esc", "done"},
				{"q", "quit"},
			}
		}
		return []keyHint{
			{"\u2191\u2193", "move"},
			{"enter", "details"},
			{"/", "search"},
			{"esc", "back"},
			{"q", "quit"},
			{"s", "sort"},
			{"S", "dir"},
			{"f", "fit"},
			{"t", "type"},
			{"m", "density"},
			{"b", "benchmarks"},
		}
	case screenDetail:
		return []keyHint{
			{"\u2191\u2193", "quants"},
			{"enter", "sources"},
			{"d", "download"},
			{"b", "benchmarks"},
			{"esc", "back"},
			{"q", "quit"},
		}
	case screenBenchmarks:
		return []keyHint{
			{"\u2191\u2193", "scroll"},
			{"esc", "back"},
			{"q", "quit"},
		}
	case screenDownload:
		if a.downloads != nil && a.downloads.promptActive() {
			return []keyHint{
				{"c", "concurrent"},
				{"s", "queue"},
			}
		}
		return []keyHint{
			{"\u2191\u2193", "move"},
			{"x", "cancel"},
			{"c", "clear"},
			{"esc", "back"},
			{"q", "quit"},
		}
	default:
		return []keyHint{
			{"esc", "back"},
			{"q", "quit"},
		}
	}
}
