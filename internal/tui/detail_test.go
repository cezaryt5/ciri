package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
	"github.com/cezaryt5/ciri/internal/predictor"
)

func TestExploreModel_TypeAndDensityFilters(t *testing.T) {
	models := []model.Model{
		{ID: "a/generative", Display: "Generative", Type: "generative", Density: "dense", ParametersRaw: 7e9},
		{ID: "b/moe", Display: "MoE", Type: "generative", Density: "moe", ParametersRaw: 30e9},
		{ID: "c/asr", Display: "ASR", Type: "asr", Density: "dense", ParametersRaw: 1e9},
		{ID: "d/ocr", Display: "OCR", Type: "ocr", Density: "dense", ParametersRaw: 2e9},
	}
	for i := range models {
		models[i].Finalize()
	}
	pred := predictor.NewPredictor(nil, 32, models, nil)
	em := newExploreModel(pred, hardware.Specs{}, nil)

	em.typeFilter = "generative"
	em.applyFilters()
	if len(em.predictions) != 2 {
		t.Fatalf("type filter: got %d, want 2", len(em.predictions))
	}

	em.densityFilter = "moe"
	em.applyFilters()
	if len(em.predictions) != 1 || em.predictions[0].Model.ID != "b/moe" {
		t.Fatalf("density filter: got %+v", em.predictions)
	}

	em.typeFilter = "ocr"
	em.densityFilter = ""
	em.applyFilters()
	if len(em.predictions) != 1 || em.predictions[0].Model.ID != "d/ocr" {
		t.Fatalf("ocr filter: got %+v", em.predictions)
	}

	em.typeFilter = ""
	em.applyFilters()
	if len(em.predictions) != 4 {
		t.Fatalf("all filter: got %d, want 4", len(em.predictions))
	}
}

func TestDetailModel_AllFittingQuantsAndSource(t *testing.T) {
	models := []model.Model{{
		ID:            "owner/Test-7B",
		Display:       "Test 7B",
		Type:          "generative",
		Density:       "dense",
		ParametersRaw: 7e9,
		Quants: []model.Quant{
			{
				Label:        "Q8_0",
				Bits:         8,
				WeightsBytes: 8e9,
				Sources: []model.QuantSource{{
					Repo: "owner/test-q8", Preferred: true, WeightsBytes: 8e9,
				}},
			},
			{
				Label:        "Q4_K_M",
				Bits:         4,
				WeightsBytes: 4e9,
				Sources: []model.QuantSource{
					{Repo: "owner/test-q4", Preferred: true, WeightsBytes: 4e9},
					{Repo: "other/test-q4", WeightsBytes: 4.1e9},
				},
			},
		},
	}}
	models[0].Finalize()
	gpu := &hardware.GPU{Name: "RTX 3060", VRAMGB: 8, Architecture: "Ampere"}
	pred := predictor.NewPredictor(gpu, 32, models, nil)
	selected := pred.PredictAll()[0]
	d := newDetailModel(selected, pred, hardware.Specs{}, gpu)

	if d.recommended != 1 {
		t.Fatalf("recommended = %d, want 1 (only Q4_K_M fits VRAM)", d.recommended)
	}
	if d.quants[0].Quant.Label != "Q4_K_M" {
		t.Fatalf("first recommended = %q, want Q4_K_M", d.quants[0].Quant.Label)
	}
	if d.currentSource() == nil || d.currentSource().Repo != "owner/test-q4" {
		t.Fatalf("preferred source = %+v", d.currentSource())
	}
	// Cycle to the second source.
	d.detailUpdate(nil, tea.KeyMsg{Type: tea.KeyRight})
	if d.currentSource() == nil || d.currentSource().Repo != "other/test-q4" {
		t.Fatalf("after cycle source = %+v", d.currentSource())
	}
}
