package main

import (
	"fmt"
	"os"

	"github.com/cezaryt5/Can_I_Run_IT/data"
	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
	"github.com/cezaryt5/Can_I_Run_IT/internal/model"
	"github.com/cezaryt5/Can_I_Run_IT/internal/predictor"
	"github.com/cezaryt5/Can_I_Run_IT/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	gpuData, err := ciridata.FS.ReadFile("gpus.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read embedded gpus.json: %v\n", err)
		os.Exit(1)
	}
	gpuDB, err := hardware.LoadGPUDB(gpuData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load GPU database: %v\n", err)
		os.Exit(1)
	}

	result, err := hardware.GetSpecs(gpuDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Hardware detection warning: %v\n", err)
	}
	specs := result.Specs
	gpu := result.GPU

	modelsData, err := ciridata.FS.ReadFile("hf_models.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read embedded hf_models.json: %v\n", err)
		os.Exit(1)
	}
	models, err := model.LoadCatalog(modelsData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load model catalog: %v\n", err)
		os.Exit(1)
	}

	benchData, err := ciridata.FS.ReadFile("benchmark_cache.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read embedded benchmark_cache.json: %v\n", err)
		os.Exit(1)
	}
	benchDB, err := predictor.LoadBenchmarks(benchData, gpuDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Benchmark load warning: %v\n", err)
	}

	pred := predictor.NewPredictor(gpu, specs.RamAvailGB, models, benchDB)

	app := tui.NewApp(specs, gpu, models, pred, benchDB, Version)
	program := tea.NewProgram(app, tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}
