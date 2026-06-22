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

func TestLoadCatalog_Valid(t *testing.T) {
	models := []Model{
		{Name: "meta-llama/Llama-3.1-8B-Instruct", MinVRAMGB: 5.0, MinRAMGB: 8.0, ParametersRaw: 8000000000, Quantization: "Q4_K_M", IsMoE: false},
		{Name: "mistralai/Mixtral-8x7B-Instruct", MinVRAMGB: 26.0, MinRAMGB: 32.0, ParametersRaw: 46000000000, IsMoE: true, ActiveParameters: 12000000000},
	}
	raw := marshalModels(t, models)
	loaded, err := LoadCatalog(raw)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 models, got %d", len(loaded))
	}
	if loaded[0].Name != models[0].Name {
		t.Errorf("name = %q, want %q", loaded[0].Name, models[0].Name)
	}
	if loaded[0].MinVRAMGB != 5.0 {
		t.Errorf("min_vram = %f, want 5.0", loaded[0].MinVRAMGB)
	}
	if loaded[0].IsMoE {
		t.Error("Llama should not be MoE")
	}
	if !loaded[1].IsMoE {
		t.Error("Mixtral should be MoE")
	}
	if loaded[1].ActiveParameters != 12000000000 {
		t.Errorf("active_params = %d, want 12000000000", loaded[1].ActiveParameters)
	}
}

func TestLoadCatalog_InvalidJSON(t *testing.T) {
	_, err := LoadCatalog([]byte("not json{{{"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadCatalog_MultipleCategories(t *testing.T) {
	models := []Model{
		{
			Name:   "meta-llama/Llama-3.2-11B-Vision-Instruct",
			UseCase: "Multimodal, vision and text",
			Capabilities: []string{"vision"},
		},
	}
	raw := marshalModels(t, models)
	loaded, err := LoadCatalog(raw)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 model, got %d", len(loaded))
	}

	// Should have Vision (from capabilities + use_case) but also General (since it's text-generation)
	cats := loaded[0].Categories
	if len(cats) == 0 {
		t.Fatal("expected at least 1 category")
	}
	hasVision := false
	for _, c := range cats {
		if c == CategoryVision {
			hasVision = true
		}
	}
	if !hasVision {
		t.Errorf("expected Vision category, got %v", cats)
	}
}

func TestCategorize_Coding(t *testing.T) {
	m := Model{UseCase: "Code generation and completion"}
	Categorize(&m)
	if len(m.Categories) == 0 || m.Categories[0] != CategoryCoding {
		t.Errorf("expected Coding, got %v", m.Categories)
	}
}

func TestCategorize_CodingAdvanced(t *testing.T) {
	m := Model{UseCase: "Advanced reasoning, math and code"}
	Categorize(&m)
	hasCoding := false
	for _, c := range m.Categories {
		if c == CategoryCoding {
			hasCoding = true
		}
	}
	if !hasCoding {
		t.Errorf("expected Coding, got %v", m.Categories)
	}
}

func TestCategorize_Chat(t *testing.T) {
	m := Model{UseCase: "Instruction following, chat"}
	Categorize(&m)
	if len(m.Categories) == 0 || m.Categories[0] != CategoryChat {
		t.Errorf("expected Chat, got %v", m.Categories)
	}
}

func TestCategorize_VisionByCapability(t *testing.T) {
	m := Model{UseCase: "General purpose", Capabilities: []string{"vision"}}
	Categorize(&m)
	hasVision := false
	for _, c := range m.Categories {
		if c == CategoryVision {
			hasVision = true
		}
	}
	if !hasVision {
		t.Errorf("expected Vision, got %v", m.Categories)
	}
}

func TestCategorize_VisionByPipeline(t *testing.T) {
	m := Model{UseCase: "Multimodal, on-device (effective 2B)", PipelineTag: "image-text-to-text"}
	Categorize(&m)
	hasVision := false
	for _, c := range m.Categories {
		if c == CategoryVision {
			hasVision = true
		}
	}
	if !hasVision {
		t.Errorf("expected Vision from pipeline_tag, got %v", m.Categories)
	}
}

func TestCategorize_Translation(t *testing.T) {
	m := Model{UseCase: "Audio transcription (state-of-the-art accuracy)", PipelineTag: "automatic-speech-recognition"}
	Categorize(&m)
	if len(m.Categories) == 0 || m.Categories[0] != CategoryTranslation {
		t.Errorf("expected Translation, got %v", m.Categories)
	}
}

func TestCategorize_General(t *testing.T) {
	m := Model{UseCase: "General purpose"}
	Categorize(&m)
	if len(m.Categories) == 0 || m.Categories[0] != CategoryGeneral {
		t.Errorf("expected General, got %v", m.Categories)
	}
}

func TestCategorize_GeneralEdgeDeploy(t *testing.T) {
	m := Model{UseCase: "Lightweight, edge deployment"}
	Categorize(&m)
	if len(m.Categories) == 0 || m.Categories[0] != CategoryGeneral {
		t.Errorf("expected General, got %v", m.Categories)
	}
}

func TestAllCategories_Order(t *testing.T) {
	expected := []Category{
		CategoryCoding,
		CategoryChat,
		CategoryGeneral,
		CategoryVision,
		CategoryTranslation,
	}
	got := AllCategories()
	if len(got) != len(expected) {
		t.Fatalf("len = %d, want %d", len(got), len(expected))
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], expected[i])
		}
	}
}

func TestLoadCatalog_CaseInsensitiveCategoryMatch(t *testing.T) {
	models := []Model{
		{Name: "test-model", UseCase: "INSTRUCTION FOLLOWING, CHAT"},
	}
	raw := marshalModels(t, models)
	loaded, err := LoadCatalog(raw)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(loaded[0].Categories) == 0 || loaded[0].Categories[0] != CategoryChat {
		t.Errorf("case-insensitive chat match failed, got %v", loaded[0].Categories)
	}
}

func TestLoadCatalog_EmptyFile(t *testing.T) {
	models := []Model{}
	raw := marshalModels(t, models)
	loaded, err := LoadCatalog(raw)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 models, got %d", len(loaded))
	}
}

func TestLoadCatalog_IntVRAMValues(t *testing.T) {
	// JSON numbers may be int or float — both must unmarshal into float64.
	raw := []byte(`[{"name":"test","min_vram_gb":8,"min_ram_gb":16,"use_case":"General purpose"}]`)
	loaded, err := LoadCatalog(raw)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if loaded[0].MinVRAMGB != 8.0 {
		t.Errorf("min_vram (int→float64): got %f, want 8.0", loaded[0].MinVRAMGB)
	}
	if loaded[0].MinRAMGB != 16.0 {
		t.Errorf("min_ram (int→float64): got %f, want 16.0", loaded[0].MinRAMGB)
	}
}
