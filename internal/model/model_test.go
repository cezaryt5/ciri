package model

import (
	"encoding/json"
	"testing"
)

func marshalModels(t *testing.T, models []Model) []byte {
	t.Helper()
	raw, err := json.Marshal(models)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestLoadCatalog_Legacy(t *testing.T) {
	models := []Model{
		{Name: "meta-llama/Llama-3.1-8B-Instruct", MinVRAMGB: 5.0, MinRAMGB: 8.0, ParametersRaw: 8000000000, Quantization: "Q4_K_M", PipelineTag: "text-generation"},
		{Name: "mistralai/Mixtral-8x7B-Instruct", MinVRAMGB: 26.0, MinRAMGB: 32.0, ParametersRaw: 46000000000, IsMoE: true, ActiveParameters: 12000000000, PipelineTag: "text-generation"},
	}
	loaded, err := LoadCatalog(marshalModels(t, models))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 models, got %d", len(loaded))
	}
	if loaded[0].Type != "generative" {
		t.Errorf("type = %q, want generative", loaded[0].Type)
	}
	if loaded[0].Density != "dense" {
		t.Errorf("density = %q, want dense", loaded[0].Density)
	}
	if loaded[1].Density != "moe" || !loaded[1].IsMoE {
		t.Errorf("expected MoE, got %+v", loaded[1])
	}
	if loaded[1].ActiveParameters != 12000000000 {
		t.Errorf("active_params = %d, want 12000000000", loaded[1].ActiveParameters)
	}
	if loaded[0].BenchmarkIDs[0] != models[0].Name {
		t.Errorf("benchmark IDs = %v", loaded[0].BenchmarkIDs)
	}
}

func TestLoadCatalog_InvalidJSON(t *testing.T) {
	if _, err := LoadCatalog([]byte("not json{{{")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadCatalogV2(t *testing.T) {
	raw := []byte(`{
		"schema": 5,
		"models": [{
			"id": "Qwen/Qwen3.8-27B",
			"display": "Qwen3.8 27B",
			"provider": "Qwen",
			"params_total": 27800000000,
			"type": "generative",
			"density": "moe",
			"arch": "qwen35moe",
			"moe": true,
			"experts": 8,
			"experts_active": 3,
			"ctx_train": 32768,
			"downloads": 1234,
			"likes": 56,
			"quants": [{
				"label": "Q8_0",
				"bits": 8,
				"weights_bytes": 9000,
				"download_bytes": 9500,
				"sources": [{"repo": "unsloth/Qwen3.8-27B-GGUF", "downloads": 10, "preferred": true, "weights_bytes": 9000, "files": [{"path": "q8.gguf", "size": 9000}]}]
			}, {
				"label": "Q4_K_M",
				"bits": 4,
				"weights_bytes": 4200,
				"download_bytes": 4400,
				"sources": [{"repo": "bartowski/Qwen3.8-27B-GGUF", "downloads": 20, "preferred": true, "weights_bytes": 4200, "files": [{"path": "q4.gguf", "size": 4200}]}]
			}]
		}]
	}`)
	models, err := LoadCatalogV2(raw)
	if err != nil {
		t.Fatalf("LoadCatalogV2: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	m := models[0]
	if m.ParameterCount != "27.8B" {
		t.Errorf("parameter count = %q, want 27.8B", m.ParameterCount)
	}
	if m.Quantization != "Q4_K_M" {
		t.Errorf("default quant = %q, want Q4_K_M", m.Quantization)
	}
	if m.WeightsBytes != 4200 {
		t.Errorf("weights bytes = %d, want 4200", m.WeightsBytes)
	}
	if m.QuantCount != 2 {
		t.Errorf("quant count = %d, want 2", m.QuantCount)
	}
	if m.ActiveParameters != 27800000000*3/8 {
		t.Errorf("active params = %d", m.ActiveParameters)
	}
	if len(m.BenchmarkIDs) != 2 {
		t.Errorf("benchmark IDs = %v, want 2 repos", m.BenchmarkIDs)
	}
	if q := m.Quants[0].PreferredSource(); q == nil || q.Repo != "unsloth/Qwen3.8-27B-GGUF" {
		t.Errorf("preferred source = %+v", q)
	}
}

func TestLoadCatalogV2_WrongSchema(t *testing.T) {
	if _, err := LoadCatalogV2([]byte(`{"schema":4,"models":[]}`)); err == nil {
		t.Fatal("expected schema error")
	}
}

func TestInferType(t *testing.T) {
	cases := []struct {
		m    Model
		want string
	}{
		{Model{PipelineTag: "automatic-speech-recognition"}, "asr"},
		{Model{PipelineTag: "text-to-speech"}, "tts"},
		{Model{PipelineTag: "image-text-to-text"}, "vlm"},
		{Model{PipelineTag: "text-to-image"}, "diffusion"},
		{Model{PipelineTag: "feature-extraction"}, "embedding"},
		{Model{PipelineTag: "text-generation"}, "generative"},
		{Model{ID: "deepseek-ai/DeepSeek-OCR"}, "ocr"},
	}
	for _, c := range cases {
		if got := inferType(&c.m); got != c.want {
			t.Errorf("inferType(%+v) = %q, want %q", c.m, got, c.want)
		}
	}
}
