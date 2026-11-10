package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// QuantFile is one downloadable file inside a quant source.
type QuantFile struct {
	Path   string `json:"path"`
	Quant  string `json:"quant"`
	Role   string `json:"role"`
	Bits   int    `json:"bits"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// QuantSource is one repository that publishes a quant.
type QuantSource struct {
	Repo          string      `json:"repo"`
	Publisher     string      `json:"publisher"`
	Downloads     int         `json:"downloads"`
	Likes         int         `json:"likes"`
	Preferred     bool        `json:"preferred"`
	WeightsBytes  int64       `json:"weights_bytes"`
	DownloadBytes int64       `json:"download_bytes"`
	FileCount     int         `json:"file_count"`
	Shards        int         `json:"shards"`
	Files         []QuantFile `json:"files"`
	AuxFiles      []QuantFile `json:"aux_files"`
	OtherFiles    []QuantFile `json:"other_files"`
}

// Quant is one quantization of a canonical model, e.g. Q4_K_M.
type Quant struct {
	Label           string        `json:"label"`
	Bits            int           `json:"bits"`
	WeightsBytes    int64         `json:"weights_bytes"`
	DownloadBytes   int64         `json:"download_bytes"`
	MinWeightsBytes int64         `json:"min_weights_bytes"`
	MaxWeightsBytes int64         `json:"max_weights_bytes"`
	FileCount       int           `json:"file_count"`
	Shards          int           `json:"shards"`
	Sources         []QuantSource `json:"sources"`
}

// PreferredSource returns the preferred publisher for this quant, falling
// back to the first source when the catalog did not mark one.
func (q Quant) PreferredSource() *QuantSource {
	for i := range q.Sources {
		if q.Sources[i].Preferred {
			return &q.Sources[i]
		}
	}
	if len(q.Sources) > 0 {
		return &q.Sources[0]
	}
	return nil
}

// SourceRepos returns every repo that publishes this quant.
func (q Quant) SourceRepos() []string {
	out := make([]string, 0, len(q.Sources))
	for _, s := range q.Sources {
		if s.Repo != "" {
			out = append(out, s.Repo)
		}
	}
	return out
}

// Model is the canonical model shown on the first catalog screen. The flat
// fields are retained so hf_models.json remains a valid offline fallback.
type Model struct {
	// v2 catalog fields.
	ID              string  `json:"id"`
	Display         string  `json:"display"`
	Type            string  `json:"type"`
	Density         string  `json:"density"`
	ParamsTotal     int64   `json:"params_total"`
	Downloads       int     `json:"downloads"`
	Likes           int     `json:"likes"`
	Arch            string  `json:"arch"`
	MoE             *bool   `json:"moe"`
	Experts         int     `json:"experts"`
	ExpertsActive   int     `json:"experts_active"`
	NLayers         int     `json:"n_layers"`
	NLayersKV       int     `json:"n_layers_kv"`
	NKVHeads        int     `json:"n_kv_heads"`
	HeadDim         int     `json:"head_dim"`
	KVBytesPerToken int64   `json:"kv_bytes_per_token"`
	KVDtype         string  `json:"kv_dtype"`
	CtxTrain        int     `json:"ctx_train"`
	License         string  `json:"license"`
	Quants          []Quant `json:"quants,omitempty"`

	// hf_models.json fields / derived compatibility fields.
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

	// Runtime-only derived values.
	WeightsBytes int64    `json:"-"`
	QuantCount   int      `json:"-"`
	BenchmarkIDs []string `json:"-"`
}

type catalogFile struct {
	Schema int     `json:"schema"`
	Models []Model `json:"models"`
}

// LoadCatalog loads the legacy flat hf_models.json catalog.
func LoadCatalog(data []byte) ([]Model, error) {
	var models []Model
	if err := json.Unmarshal(data, &models); err != nil {
		return nil, err
	}
	for i := range models {
		models[i].Finalize()
	}
	return models, nil
}

// LoadCatalogV2 loads the model -> quant -> source catalog.
func LoadCatalogV2(data []byte) ([]Model, error) {
	var doc catalogFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Schema != 5 {
		return nil, fmt.Errorf("catalog schema = %d, want 5", doc.Schema)
	}
	for i := range doc.Models {
		doc.Models[i].Finalize()
	}
	return doc.Models, nil
}

// Finalize fills the compatibility fields the predictor and TUI expect. It is
// idempotent and works for both the legacy flat catalog and schema 5.
func (m *Model) Finalize() {
	if m.ID == "" {
		m.ID = m.Name
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	if m.Display == "" {
		m.Display = displayOf(m.ID)
	}
	if m.Provider == "" {
		m.Provider = ownerOf(m.ID)
	}
	if m.ParametersRaw == 0 {
		m.ParametersRaw = m.ParamsTotal
	}
	if m.ParamsTotal == 0 {
		m.ParamsTotal = m.ParametersRaw
	}
	if m.ParameterCount == "" {
		m.ParameterCount = humanParams(m.ParametersRaw)
	}
	if m.Architecture == "" {
		m.Architecture = m.Arch
	}
	if m.Arch == "" {
		m.Arch = m.Architecture
	}
	if m.ContextLength == 0 {
		m.ContextLength = m.CtxTrain
	}
	if m.CtxTrain == 0 {
		m.CtxTrain = m.ContextLength
	}
	if m.HfDownloads == 0 {
		m.HfDownloads = m.Downloads
	}
	if m.Downloads == 0 {
		m.Downloads = m.HfDownloads
	}
	if m.HfLikes == 0 {
		m.HfLikes = m.Likes
	}
	if m.Likes == 0 {
		m.Likes = m.HfLikes
	}
	if m.Type == "" {
		m.Type = inferType(m)
	}
	m.Type = strings.ToLower(strings.TrimSpace(m.Type))
	if m.Density == "" {
		m.Density = inferDensity(m)
	}
	m.Density = strings.ToLower(strings.TrimSpace(m.Density))
	if m.MoE != nil {
		m.IsMoE = *m.MoE
	}
	if m.IsMoE {
		m.Density = "moe"
	} else if m.Density == "moe" {
		m.IsMoE = true
	}
	if m.ActiveParameters == 0 && m.ParametersRaw > 0 {
		if m.Experts > 0 && m.ExpertsActive > 0 {
			m.ActiveParameters = m.ParametersRaw * int64(m.ExpertsActive) / int64(m.Experts)
		} else {
			m.ActiveParameters = m.ParametersRaw
		}
	}

	if len(m.Quants) > 0 {
		m.QuantCount = len(m.Quants)
		q := defaultQuant(m.Quants)
		m.Quantization = q.Label
		m.WeightsBytes = q.WeightsBytes
		if m.Format == "" {
			m.Format = "GGUF"
		}
		if m.MinVRAMGB == 0 {
			m.MinVRAMGB = float64(q.WeightsBytes) / 1e9
		}
		if m.MinRAMGB == 0 {
			m.MinRAMGB = m.MinVRAMGB*1.15 + 2
			m.RecommendedRAMGB = m.MinRAMGB + 4
		}
		seen := map[string]bool{}
		for _, qq := range m.Quants {
			for _, repo := range qq.SourceRepos() {
				if !seen[repo] {
					seen[repo] = true
					m.BenchmarkIDs = append(m.BenchmarkIDs, repo)
				}
			}
		}
	}
	if len(m.BenchmarkIDs) == 0 && strings.Contains(m.Name, "/") {
		m.BenchmarkIDs = []string{m.Name}
	}
}

func defaultQuant(quants []Quant) Quant {
	preferred := []string{"Q4_K_M", "Q5_K_M", "Q4_K_S", "Q5_K_S", "Q6_K", "Q8_0", "Q4_0", "Q5_0"}
	for _, want := range preferred {
		for _, q := range quants {
			if strings.EqualFold(q.Label, want) {
				return q
			}
		}
	}
	best := quants[0]
	for _, q := range quants[1:] {
		if q.WeightsBytes > 0 && (best.WeightsBytes == 0 || q.WeightsBytes < best.WeightsBytes) {
			best = q
		}
	}
	return best
}

func inferDensity(m *Model) string {
	if m.MoE != nil {
		if *m.MoE {
			return "moe"
		}
		return "dense"
	}
	if m.IsMoE || m.Experts > 0 {
		return "moe"
	}
	if m.ID != "" || m.ParametersRaw > 0 {
		return "dense"
	}
	return "unknown"
}

func inferType(m *Model) string {
	p := strings.ToLower(m.PipelineTag)
	text := strings.ToLower(m.ID + " " + m.Display + " " + m.UseCase + " " + strings.Join(m.Capabilities, " "))
	if strings.Contains(text, "ocr") || strings.Contains(text, "document") ||
		strings.Contains(text, "paddleocr") || strings.Contains(text, "olmocr") ||
		strings.Contains(text, "dots.ocr") || strings.Contains(text, "pdf") {
		return "ocr"
	}
	switch {
	case strings.Contains(p, "automatic-speech-recognition"),
		strings.Contains(p, "speech-recognition"),
		strings.Contains(p, "speech-translation"):
		return "asr"
	case strings.Contains(p, "text-to-speech"),
		strings.Contains(p, "text-to-audio"),
		strings.Contains(p, "text-to-music"):
		return "tts"
	case strings.Contains(p, "text-to-image"),
		strings.Contains(p, "image-to-image"),
		strings.Contains(p, "text-to-video"),
		strings.Contains(p, "image-to-video"):
		return "diffusion"
	case strings.Contains(p, "feature-extraction"),
		strings.Contains(p, "sentence-similarity"):
		return "embedding"
	case strings.Contains(p, "image-text-to-text"),
		strings.Contains(p, "image-to-text"),
		strings.Contains(p, "visual-question-answering"),
		strings.Contains(p, "document-question-answering"),
		strings.Contains(p, "any-to-any"),
		strings.Contains(p, "video-text-to-text"):
		return "vlm"
	case strings.Contains(p, "audio"):
		return "audio"
	case strings.Contains(p, "text-generation"), strings.Contains(p, "conversational"):
		return "generative"
	default:
		if strings.Contains(text, "vision") || strings.Contains(text, "multimodal") {
			return "vlm"
		}
		return "other"
	}
}

func displayOf(id string) string {
	name := id
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, "-GGUF")
	name = strings.TrimSuffix(name, "-gguf")
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	return strings.TrimSpace(name)
}

func ownerOf(id string) string {
	if i := strings.Index(id, "/"); i > 0 {
		return id[:i]
	}
	return ""
}

func humanParams(n int64) string {
	if n <= 0 {
		return ""
	}
	units := []struct {
		v float64
		s string
	}{{1e12, "T"}, {1e9, "B"}, {1e6, "M"}, {1e3, "K"}}
	for _, u := range units {
		if float64(n) >= u.v {
			return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", float64(n)/u.v), "0"), ".") + u.s
		}
	}
	return fmt.Sprintf("%d", n)
}
