package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
	"github.com/cezaryt5/Can_I_Run_IT/internal/model"
	"github.com/cezaryt5/Can_I_Run_IT/internal/predictor"
	"github.com/cezaryt5/Can_I_Run_IT/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// main — cmd/ciri/main.go:16
// Called from: Go runtime (automatic)
// Application entry point. Resolves the data directory, loads the GPU database
// (gpus.json), detects hardware (CPU, RAM, GPU), loads the model catalog
// (hf_models.json) and benchmark cache (benchmark_cache.json), creates a
// Predictor, and launches the Bubble Tea TUI.
func main() {
	exe, _ := os.Executable()
	baseDir := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(baseDir, "data")); os.IsNotExist(err) {
		baseDir, _ = os.Getwd()
	}

	dataDir := filepath.Join(baseDir, "data")
	gpuPath := filepath.Join(dataDir, "gpus.json")
	modelsPath := filepath.Join(dataDir, "hf_models.json")
	benchPath := filepath.Join(dataDir, "benchmark_cache.json")

	// 1. Load GPU database
	gpuDB, err := hardware.LoadGPUDB(gpuPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load GPU database: %v\n", err)
		os.Exit(1)
	}

	// 2. Detect hardware
	result, err := hardware.GetSpecs(gpuDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Hardware detection warning: %v\n", err)
	}
	specs := result.Specs
	gpu := result.GPU

	// 3. Load model catalog
	models, err := model.LoadCatalog(modelsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load model catalog: %v\n", err)
		os.Exit(1)
	}

	// 4. Load benchmarks
	benchDB, err := predictor.LoadBenchmarks(benchPath, gpuDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Benchmark load warning: %v\n", err)
	}

	// 5. Create predictor
	pred := predictor.NewPredictor(gpu, specs.RamAvailGB, models, benchDB)

	// 6. Start TUI
	app := tui.NewApp(specs, gpu, models, pred, benchDB, Version)
	program := tea.NewProgram(app, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}
