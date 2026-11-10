package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"
)

func testApp(version string) *App {
	gpuDB := []hardware.GPU{
		{Name: "RTX 4090", VRAMGB: 24, Architecture: "Ada"},
	}
	models := []model.Model{
		{
			Name:           "test-model",
			Provider:       "test",
			ParameterCount: "8B",
			ParametersRaw:  8_000_000_000,
			MinVRAMGB:      4,
			MinRAMGB:       8,
			Quantization:   "Q4_K_M",
			Format:         "safetensors",
			ContextLength:  8192,
			ReleaseDate:    "2024-07-01",
		},
	}
	pred := predictor.NewPredictor(&gpuDB[0], 32, models, nil)
	specs := hardware.Specs{
		CpuModel:    "Test CPU",
		CpuCores:    8,
		RamAvailGB:  32,
		RamTotalGB:  64,
		HasOllama:   true,
		HasLlamaCPP: false,
	}
	app := NewApp(specs, &gpuDB[0], models, pred, nil, version)
	app.width = 80
	app.height = 24
	return app
}

func TestApp_VersionStored(t *testing.T) {
	app := testApp("1.0.0")
	if app.version != "1.0.0" {
		t.Errorf("expected version \"1.0.0\", got %q", app.version)
	}
}

func TestApp_VersionEmptyDefault(t *testing.T) {
	app := testApp("")
	if app.version != "" {
		t.Errorf("expected empty version, got %q", app.version)
	}
}

func TestApp_ViewContainsBrandAndVersion(t *testing.T) {
	view := testApp("1.0.0").View()
	if !strings.Contains(view, "CIRI") {
		t.Errorf("expected View to contain the CIRI wordmark, got:\n%s", view)
	}
	if !strings.Contains(view, "v1.0.0") {
		t.Errorf("expected View to contain \"v1.0.0\", got:\n%s", view)
	}
}

func TestApp_ViewOmitsVersionWhenEmpty(t *testing.T) {
	view := testApp("").View()
	if !strings.Contains(view, "CIRI") {
		t.Errorf("expected View to contain \"CIRI\", got:\n%s", view)
	}
	if strings.Contains(view, "v1.0.0") {
		t.Errorf("expected View NOT to contain a version when version is empty, got:\n%s", view)
	}
}

func TestApp_ViewFitsTerminal(t *testing.T) {
	for _, width := range []int{60, 80, 120} {
		app := testApp("1.0.0")
		app.width = width
		app.height = 24

		app.screen = screenExplore
		app.explore = newExploreModel(app.pred, app.specs, app.gpu)
		first := app.explore.predictions[0]

		screens := []struct {
			name   string
			screen screen
			setup  func()
		}{
			{"home", screenHome, func() {}},
			{"explore", screenExplore, func() {
				app.explore = newExploreModel(app.pred, app.specs, app.gpu)
			}},
			{"detail", screenDetail, func() {
				app.detail = &detailModel{selected: first, specs: app.specs, gpu: app.gpu}
			}},
			{"benchmarks", screenBenchmarks, func() {
				app.detail = &detailModel{selected: first, specs: app.specs, gpu: app.gpu}
				app.bench = newBenchmarksModel(first, app.gpu, nil, app.specs)
			}},
			{"settings", screenSettings, func() {
				app.settings = &settingsModel{specs: app.specs, gpu: app.gpu}
			}},
			{"download", screenDownload, func() {}},
			{"local", screenLocal, func() {}},
		}

		for _, sc := range screens {
			sc.setup()
			app.screen = sc.screen
			view := app.View()
			lines := strings.Split(view, "\n")
			if len(lines) > app.height {
				t.Errorf("%s at width %d: %d lines exceeds height %d", sc.name, width, len(lines), app.height)
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w > width {
					t.Errorf("%s at width %d: line %d is %d cells wide: %q", sc.name, width, i, w, line)
				}
			}
		}
	}
}

func TestApp_ChromeKeepsFullCPUNameAndIdleMetrics(t *testing.T) {
	app := testApp("1.0.0")
	app.width = 160
	app.height = 24
	app.screen = screenExplore
	app.explore = newExploreModel(app.pred, app.specs, app.gpu)
	app.specs.CpuModel = "Intel(R) Core(TM) i7-7700HQ CPU @ 2.80GHz"
	app.metrics = hardware.Metrics{
		CPUPercent: 0, CPUPercentKnown: true,
		GPUUtilPct: 0, GPUUtilKnown: true,
	}

	view := app.View()
	if !strings.Contains(view, "Intel(R) Core(TM) i7-7700HQ CPU @ 2.80GHz") {
		t.Fatalf("chrome truncated the CPU name:\n%s", view)
	}
	if !strings.Contains(view, "0%") {
		t.Fatalf("idle percentages must still render, got:\n%s", view)
	}
}

// testAppRows builds an app whose catalog holds count models, so list-heavy
// screens render with scrolling — exactly what counts the fixed chrome.
func testAppRows(count int) (*App, predictor.ModelPrediction) {
	gpuDB := []hardware.GPU{{Name: "RTX 4090", VRAMGB: 24, Architecture: "Ada"}}
	var models []model.Model
	for i := 0; i < count; i++ {
		models = append(models, model.Model{
			Name:           fmt.Sprintf("model-%02d", i),
			Provider:       "test",
			ParameterCount: "8B",
			ParametersRaw:  8_000_000_000,
			MinVRAMGB:      4,
			MinRAMGB:       8,
			ReleaseDate:    "2024-07-01",
		})
	}
	pred := predictor.NewPredictor(&gpuDB[0], 32, models, nil)
	specs := hardware.Specs{
		CpuModel:    "Test CPU",
		CpuCores:    8,
		RamAvailGB:  32,
		RamTotalGB:  64,
		HasOllama:   true,
		HasLlamaCPP: false,
	}
	app := NewApp(specs, &gpuDB[0], models, pred, nil, "1.0.0")
	app.width = 80
	app.height = 43
	app.explore = newExploreModel(pred, specs, &gpuDB[0])
	return app, app.explore.predictions[0]
}

// TestApp_ViewFitsTerminalWithScrolls re-checks fit when every list-screen
// renders at full scroll depth, so the fixed chrome count stays honest.
func TestApp_ViewFitsTerminalWithScrolls(t *testing.T) {
	for _, height := range []int{24, 30, 43} {
		app, first := testAppRows(40)
		app.height = height

		app.detail = &detailModel{selected: first, specs: app.specs, gpu: app.gpu}
		screens := []struct {
			name   string
			screen screen
			setup  func()
		}{
			{"explore", screenExplore, func() {}},
			{"benchmarks", screenBenchmarks, func() {}},
		}

		for _, sc := range screens {
			sc.setup()
			app.screen = sc.screen
			view := app.View()
			lines := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
			if len(lines) > app.height {
				t.Errorf("%s at height %d: %d lines exceeds height %d", sc.name, height, len(lines), app.height)
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w > app.width {
					t.Errorf("%s at height %d: line %d is %d cells wide: %q", sc.name, height, i, w, line)
				}
			}
		}
	}
}
