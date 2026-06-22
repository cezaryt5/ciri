package model

import (
	"encoding/json"
)

// Model represents a single LLM entry from hf_models.json.
type Model struct {
	Name             string   `json:"name"`
	Provider         string   `json:"provider"`
	ParameterCount   string   `json:"parameter_count"`
	ParametersRaw    int64    `json:"parameters_raw"`
	MinRAMGB         float64  `json:"min_ram_gb"`
	RecommendedRAMGB float64  `json:"recommended_ram_gb"`
	MinVRAMGB        float64  `json:"min_vram_gb"`
	Quantization     string   `json:"quantization"`
	Format           string   `json:"format"`
	ContextLength    int      `json:"context_length"`
	UseCase          string   `json:"use_case"`
	Architecture     string   `json:"architecture"`
	PipelineTag      string   `json:"pipeline_tag"`
	HfDownloads      int      `json:"hf_downloads"`
	HfLikes          int      `json:"hf_likes"`
	IsMoE            bool     `json:"is_moe"`
	ActiveParameters int64    `json:"active_parameters"`
	ReleaseDate      string   `json:"release_date"`
	Capabilities     []string `json:"capabilities"`

	Categories []Category `json:"-"`
}

// LoadCatalog — internal/model/catalog.go:35
// Called from: cmd/ciri/main.go:44; model_test.go:31,56,67,83,213,225,241
// Reads hf_models.json, unmarshals it into []Model, and pre-computes
// Categories for each model via Categorize().
func LoadCatalog(data []byte) ([]Model, error) {
	var models []Model
	if err := json.Unmarshal(data, &models); err != nil {
		return nil, err
	}

	for i := range models {
		Categorize(&models[i])
	}

	return models, nil
}
