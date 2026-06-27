package tui

import (
	"fmt"
	"strings"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	screenHome screen = iota
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
	home        *homeModel
	explore     *exploreModel
	detail      *detailModel
	bench       *benchmarksModel
	download    *downloadModel
	local       *localModel
	settings    *settingsModel

	specs   hardware.Specs
	gpu     *hardware.GPU
	models  []model.Model
	pred    *predictor.Predictor
	benchDB *predictor.BenchmarkDB
	version string
}

var ciriLogo = []string{
	`   ██████╗ ██╗██████╗ ██╗`,
	`  ██╔════╝ ██║██╔══██╗██║`,
	`  ██║      ██║██████╔╝██║`,
	`  ██║      ██║██╔══██╗██║`,
	`  ╚██████╗ ██║██║  ██║██║`,
	`   ╚═════╝ ╚═╝╚═╝  ╚═╝╚═╝`,
}

var (
	arrowStyle = lipgloss.NewStyle().Foreground(cyan)
	checkStyle = lipgloss.NewStyle().Foreground(green)
	crossStyle = lipgloss.NewStyle().Foreground(red)
)

func NewApp(specs hardware.Specs, gpu *hardware.GPU, models []model.Model, pred *predictor.Predictor, benchDB *predictor.BenchmarkDB, version string) *App {
	return &App{
		screen:  screenHome,
		home:    newHomeModel(),
		specs:   specs,
		gpu:     gpu,
		models:  models,
		pred:    pred,
		benchDB: benchDB,
		version: version,
	}
}

func (a *App) Init() tea.Cmd {
	return nil
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		return a, nil

	case navigateMsg:
		if msg.target == screenBenchmarks {
			a.benchOrigin = a.screen
		}
		a.screen = msg.target
		if a.screen == screenBenchmarks && a.detail != nil {
			a.bench = newBenchmarksModel(a.detail.selected, a.gpu, a.benchDB, a.specs)
		}
		return a, nil

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
		if msg.String() == "q" && !a.isTextInput() {
			return a, tea.Quit
		}
		if a.screen == screenHome {
			return a, a.home.homeUpdate(a, msg)
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
		if a.screen == screenDownload && a.download != nil {
			return a, a.download.downloadUpdate(a, msg)
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
	var sb strings.Builder

	if a.screen == screenHome {
		sb.WriteString(renderLogoHeader(a) + "\n")
	}

	title := "CIRI"
	if a.version != "" {
		title = "CIRI v" + a.version
	}
	headerContent := RenderHardwareBar(a.specs, a.gpu, a.width)
	sb.WriteString(RenderBox(title, headerContent, a.width) + "\n")

	switch a.screen {
	case screenExplore:
		if a.explore == nil {
			a.explore = newExploreModel(a.pred, a.specs, a.gpu)
		}
		sb.WriteString(RenderBox(a.label(), a.explore.exploreView(a), a.width) + "\n")
		if len(a.explore.predictions) > 0 {
			sb.WriteString(a.explore.explorePreview() + "\n")
		}
		sb.WriteString(a.explore.exploreFooter())
	case screenHome:
		sb.WriteString(RenderBox(a.label(), a.home.homeView(a), a.width))
	case screenDetail:
		if a.detail == nil {
			a.detail = newDetailModel(a.pred, a.specs, a.gpu)
		}
		sb.WriteString(RenderBox(a.label(), a.detail.detailView(a), a.width))
	case screenBenchmarks:
		if a.bench == nil && a.detail != nil {
			a.bench = newBenchmarksModel(a.detail.selected, a.gpu, a.benchDB, a.specs)
		}
		if a.bench != nil {
			sb.WriteString(RenderBox(a.label(), a.bench.benchView(a), a.width))
		}
	case screenDownload:
		if a.download == nil {
			a.download = &downloadModel{}
		}
		sb.WriteString(RenderBox(a.label(), a.download.downloadView(a), a.width))
	case screenLocal:
		if a.local == nil {
			a.local = &localModel{}
		}
		sb.WriteString(RenderBox(a.label(), a.local.localView(a), a.width))
	case screenSettings:
		if a.settings == nil {
			a.settings = &settingsModel{specs: a.specs, gpu: a.gpu}
		}
		sb.WriteString(RenderBox(a.label(), a.settings.settingsView(a), a.width))
	}

	contentHeight := a.height - 5
	if contentHeight < 1 {
		contentHeight = 1
	}
	return lipgloss.NewStyle().Width(a.width).Height(contentHeight).Render(sb.String())
}

func (a *App) isTextInput() bool {
	if a.screen == screenExplore && a.explore != nil {
		return a.explore.searching
	}
	return false
}

func (a *App) label() string {
	switch a.screen {
	case screenHome:
		return "Home"
	case screenExplore:
		return "Explore Models"
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
	case screenDownload:
		return "Download Models"
	case screenLocal:
		return "Manage Local LLMs"
	case screenSettings:
		return "Settings"
	}
	return ""
}

func renderLogoHeader(a *App) string {
	info := hardwareInfoLines(a)
	var b strings.Builder
	for i, logoLine := range ciriLogo {
		b.WriteString(logoLine)
		b.WriteString("   ")
		b.WriteString(info[i])
		if i < len(ciriLogo)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func hardwareInfoLines(a *App) []string {
	gpuName := "Unknown"
	vramGB := 0.0
	if a.gpu != nil {
		gpuName = a.gpu.Name
		vramGB = a.gpu.VRAMGB
	}

	cpuModel := a.specs.CpuModel
	if cpuModel == "" {
		cpuModel = "Unknown"
	}

	arrow := arrowStyle.Render(">")
	check := checkStyle.Render("\u2713")
	cross := crossStyle.Render("\u00d7")

	ollamaStr := cross
	if a.specs.HasOllama {
		ollamaStr = check
	}
	llamaStr := cross
	if a.specs.HasLlamaCPP {
		llamaStr = check
	}

	return []string{
		fmt.Sprintf("%s GPU:    %s", arrow, gpuName),
		fmt.Sprintf("%s VRAM:   %.1f GB", arrow, vramGB),
		fmt.Sprintf("%s RAM:    %.1f / %.1f GB", arrow, a.specs.RamAvailGB, a.specs.RamTotalGB),
		fmt.Sprintf("%s CPU:    %s (%dc)", arrow, cpuModel, a.specs.CpuCores),
		fmt.Sprintf("%s Ollama: %s", arrow, ollamaStr),
		fmt.Sprintf("%s llama.cpp: %s", arrow, llamaStr),
	}
}

