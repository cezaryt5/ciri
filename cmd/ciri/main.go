package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cezaryt5/ciri/data"
	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"
	"github.com/cezaryt5/ciri/internal/tui"

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

	catalogPath := flag.String("catalog", os.Getenv("CIRI_CATALOG"),
		"path to a schema-5 model/quant catalog (default: embedded hf_models.json)")
	flag.Parse()

	models, err := loadModels(*catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load model catalog: %v\n", err)
		os.Exit(1)
	}

	benchData, err := ciridata.FS.ReadFile("benchmark_cache.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read embedded benchmark_cache.json: %v\n", err)
		os.Exit(1)
	}
	benchDB, err := predictor.LoadBenchmarks(benchData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Benchmark load warning: %v\n", err)
	}

	pred := predictor.NewPredictor(gpu, specs.RamAvailGB, models, benchDB)

	app := tui.NewApp(specs, gpu, models, pred, benchDB, Version)
	program := tea.NewProgram(app, tea.WithAltScreen())

	restoreBackground := tui.BlackTerminalBackground()
	_, runErr := program.Run()
	restoreBackground()
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", runErr)
		os.Exit(1)
	}
}

func loadModels(path string) ([]model.Model, error) {
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return model.LoadCatalogV2(raw)
	}

	if blob, err := ciridata.FS.ReadFile("catalog_v2.json.gz"); err == nil {
		zr, zerr := gzip.NewReader(bytes.NewReader(blob))
		if zerr == nil {
			raw, rerr := io.ReadAll(zr)
			_ = zr.Close()
			if rerr == nil {
				if models, merr := model.LoadCatalogV2(raw); merr == nil {
					return models, nil
				} else {
					fmt.Fprintf(os.Stderr, "embedded catalog_v2 invalid: %v; using legacy catalog\n", merr)
				}
			}
		}
	}

	raw, err := ciridata.FS.ReadFile("hf_models.json")
	if err != nil {
		return nil, fmt.Errorf("read embedded hf_models.json: %w", err)
	}
	return model.LoadCatalog(raw)
}
