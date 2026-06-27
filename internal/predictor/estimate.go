package predictor

import (
	"encoding/json"
	"math"
	"slices"
	"strings"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
)

// ---------------------------------------------------------------------------
// Benchmark cache types (mirrors benchmark_cache.json)
// ---------------------------------------------------------------------------

type benchmarkCacheFile struct {
	ScrapedAt string                `json:"scraped_at"`
	Presets   map[string]presetData `json:"presets"`
}

type presetData struct {
	Rows []benchmarkRowJSON `json:"rows"`
}

type benchmarkRowJSON struct {
	Model         benchmarkModelJSON    `json:"model"`
	Hardware      benchmarkHardwareJSON `json:"hardware"`
	Engine        benchmarkEngineJSON   `json:"engine"`
	TokSOut       *float64              `json:"tokSOut"`
	TokSPrefill   *float64              `json:"tokSPrefill"`
	PeakVRAMGB    *float64              `json:"peakVramGb"`
	ContextLength int                   `json:"contextLength"`
	Notes         *string               `json:"notes"`
}

type benchmarkModelJSON struct {
	HfID        string `json:"hfId"`
	DisplayName string `json:"displayName"`
	Params      int    `json:"params"`
	IsMoE       bool   `json:"isMoE"`
}

type benchmarkHardwareJSON struct {
	HwClass         string   `json:"hwClass"`
	GpuName         *string  `json:"gpuName"`
	VRAMGB          *float64 `json:"vramGb"`
	ChipVendor      *string  `json:"chipVendor"`
	ChipFamily      *string  `json:"chipFamily"`
	ChipVariant     *string  `json:"chipVariant"`
	UnifiedMemoryGB *int     `json:"unifiedMemoryGb"`
	CPU             *string  `json:"cpu"`
}

type benchmarkEngineJSON struct {
	EngineName   string  `json:"engineName"`
	Quantization string  `json:"quantization"`
	Backend      *string `json:"backend"`
}

// BenchmarkRow is a single performance measurement.
type BenchmarkRow struct {
	HfID         string
	DisplayName  string
	EngineName   string
	Quantization string
	TokSOut      float64
	TokSPrefill  float64
	PeakVRAMGB   float64
	ContextLen   int
	Notes        string
}

// BenchmarkDB indexes benchmark rows by (gpuName, hfId) for quick lookups.
type BenchmarkDB struct {
	byNameHfID map[string][]BenchmarkRow // key: "gpuName|hfId"
}

// LoadBenchmarks reads benchmark_cache.json and builds the exact-match index.
func LoadBenchmarks(data []byte) (*BenchmarkDB, error) {
	var cache benchmarkCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}

	db := &BenchmarkDB{
		byNameHfID: make(map[string][]BenchmarkRow),
	}

	for presetName, preset := range cache.Presets {
		presetGPUName := extractGPUName(presetName)
		presetCanonical := hardware.NormalizeGPUName(presetGPUName)

		for _, r := range preset.Rows {
			if r.TokSOut == nil {
				continue
			}

			row := BenchmarkRow{
				HfID:         r.Model.HfID,
				DisplayName:  r.Model.DisplayName,
				EngineName:   r.Engine.EngineName,
				Quantization: r.Engine.Quantization,
				TokSOut:      *r.TokSOut,
				ContextLen:   r.ContextLength,
			}
			if r.TokSPrefill != nil {
				row.TokSPrefill = *r.TokSPrefill
			}
			if r.PeakVRAMGB != nil {
				row.PeakVRAMGB = *r.PeakVRAMGB
			}
			if r.Notes != nil {
				row.Notes = *r.Notes
			}

			// Index by preset GPU name (e.g., "RTX 5090")
			key := strings.ToLower(presetGPUName + "|" + row.HfID)
			db.byNameHfID[key] = append(db.byNameHfID[key], row)

			// Also index by canonical form (e.g., "5090") — matches
			// gpu.CanonicalName lookup in EstimateSpeed. Skip if same as preset key.
			if presetCanonical != "" {
				canonKey := strings.ToLower(presetCanonical + "|" + row.HfID)
				if canonKey != key {
					db.byNameHfID[canonKey] = append(db.byNameHfID[canonKey], row)
				}
			}

			// Index by the row's own GPU name and its canonical form if available.
			if r.Hardware.GpuName != nil {
				gpuName := *r.Hardware.GpuName
				gpuKey := strings.ToLower(gpuName + "|" + row.HfID)
				if gpuKey != key {
					db.byNameHfID[gpuKey] = append(db.byNameHfID[gpuKey], row)
				}

				gpuCanon := hardware.NormalizeGPUName(gpuName)
				if gpuCanon != "" {
					gpuCanonKey := strings.ToLower(gpuCanon + "|" + row.HfID)
					if gpuCanonKey != key && gpuCanonKey != gpuKey {
						db.byNameHfID[gpuCanonKey] = append(db.byNameHfID[gpuCanonKey], row)
					}
				}
			}
		}
	}

	return db, nil
}

// ByNameHfID returns the benchmark index by (gpuName, hfId).
func (db *BenchmarkDB) ByNameHfID() map[string][]BenchmarkRow {
	return db.byNameHfID
}

// extractGPUName strips the VRAM suffix from a benchmark preset name.
// "RTX 5090 (32 GB)" → "RTX 5090" / "Apple M4 Max (128 GB)" → "Apple M4 Max"
func extractGPUName(presetName string) string {
	name := strings.TrimSpace(presetName)
	if idx := strings.LastIndex(name, " ("); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	return name
}

// ---------------------------------------------------------------------------
// Speed estimation
// ---------------------------------------------------------------------------

const (
	ConfBenchmark = "Benchmark"
	ConfHeuristic = "Heuristic"
)

// Overhead to account for CUDA context and KV cache sizing
const vramOverheadGB = 1.5

// memoryEfficiencyByArch maps architecture families to realistic sustained
// bandwidth utilization. No GPU hits 100% of spec-sheet bandwidth in practice;
// Tensor Core / matrix-unit generations sustain higher fractions because
// dequantization is cheaper relative to memory pressure.
var memoryEfficiencyByArch = map[string]float64{
	// NVIDIA
	"ada lovelace": 0.80,
	"ampere":       0.75,
	"turing":       0.55,
	"hopper":       0.85,
	"volta":        0.60,
	"pascal":       0.50,
	"maxwell":      0.45,
	"kepler":       0.40,
	// AMD
	"rdna 3":  0.70,
	"rdna 2":  0.65,
	"rdna 1":  0.55,
	"cdna 3":  0.80,
	"cdna 2":  0.75,
	"cdna 1":  0.70,
	"vega":    0.50,
	"polaris": 0.45,
	// Apple unified memory
	"apple m4": 0.75,
	"apple m3": 0.70,
	"apple m2": 0.70,
	"apple m1": 0.65,
	// Intel
	"alchemist":  0.55,
	"battlemage": 0.60,
	"gaudi 3":    0.80,
	"gaudi 2":    0.75,
}

// Ordered slice to ensure deterministic substring fallbacks
var memoryEfficiencyKeys = []string{
	"ada lovelace", "ampere", "turing", "hopper", "volta", "pascal", "maxwell", "kepler",
	"rdna 3", "rdna 2", "rdna 1", "cdna 3", "cdna 2", "cdna 1", "vega", "polaris",
	"apple m4", "apple m3", "apple m2", "apple m1",
	"alchemist", "battlemage", "gaudi 3", "gaudi 2",
}

// computeEfficiencyByArch maps architecture families to realistic compute
// utilization for single-stream decode. While decode is memory-bound for most
// models, small models on fast GPUs can become compute-bound.
var computeEfficiencyByArch = map[string]float64{
	// NVIDIA — later Tensor Core generations sustain higher fractions
	"ada lovelace": 0.30,
	"ampere":       0.25,
	"turing":       0.20,
	"hopper":       0.35,
	"volta":        0.20,
	"pascal":       0.15,
	"maxwell":      0.12,
	"kepler":       0.10,
	// AMD
	"rdna 3":  0.22,
	"rdna 2":  0.20,
	"rdna 1":  0.15,
	"cdna 3":  0.35,
	"cdna 2":  0.28,
	"cdna 1":  0.22,
	"vega":    0.12,
	"polaris": 0.10,
	// Apple
	"apple m4": 0.35,
	"apple m3": 0.30,
	"apple m2": 0.25,
	"apple m1": 0.20,
	// Intel
	"alchemist":  0.12,
	"battlemage": 0.15,
	"gaudi 3":    0.30,
	"gaudi 2":    0.25,
}

var computeEfficiencyKeys = []string{
	"ada lovelace", "ampere", "turing", "hopper", "volta", "pascal", "maxwell", "kepler",
	"rdna 3", "rdna 2", "rdna 1", "cdna 3", "cdna 2", "cdna 1", "vega", "polaris",
	"apple m4", "apple m3", "apple m2", "apple m1",
	"alchemist", "battlemage", "gaudi 3", "gaudi 2",
}

// GetComputeEfficiency returns the compute utilization factor for a given
// GPU architecture. Unrecognized architectures get 0.20 (conservative baseline).
func GetComputeEfficiency(arch string) float64 {
	arch = strings.ToLower(strings.TrimSpace(arch))
	if arch == "" {
		return 0.20
	}
	if eff, ok := computeEfficiencyByArch[arch]; ok {
		return eff
	}
	for _, family := range computeEfficiencyKeys {
		if strings.Contains(arch, family) || strings.Contains(family, arch) {
			return computeEfficiencyByArch[family]
		}
	}
	return 0.20
}

// GetMemoryEfficiency returns the bandwidth scaling factor for a given GPU architecture.
func GetMemoryEfficiency(arch string) float64 {
	arch = strings.ToLower(strings.TrimSpace(arch))
	if arch == "" {
		return 0.60
	}
	if eff, ok := memoryEfficiencyByArch[arch]; ok {
		return eff
	}

	// Deterministic fuzzy fallback
	for _, family := range memoryEfficiencyKeys {
		if strings.Contains(arch, family) || strings.Contains(family, arch) {
			return memoryEfficiencyByArch[family]
		}
	}

	// System RAM / DDR fallback — no dedicated VRAM to optimize.
	if strings.Contains(arch, "ddr") || strings.Contains(arch, "system") {
		return 0.45
	}
	return 0.60 // Conservative baseline for unrecognized architectures.
}

// quantBytesPerParam maps exact quantization tags to bytes per parameter.
var quantBytesPerParam = map[string]float64{
	// GGUF / llama.cpp
	"Q2_K":    0.320,
	"Q2_K_S":  0.320,
	"Q3_K":    0.419,
	"Q3_K_S":  0.438,
	"Q3_K_M":  0.488,
	"Q3_K_L":  0.531,
	"Q4_0":    0.563,
	"Q4_1":    0.563,
	"Q4_K":    0.625, // Fallback common tag
	"Q4_K_S":  0.573,
	"Q4_K_M":  0.625,
	"Q4_K_L":  0.625,
	"Q5_0":    0.688,
	"Q5_1":    0.688,
	"Q5_K":    0.710, // Fallback common tag
	"Q5_K_S":  0.667,
	"Q5_K_M":  0.710,
	"Q6_K":    0.824,
	"Q8_0":    1.063,
	"Q8_K":    1.063,
	"IQ1_S":   0.195,
	"IQ1_M":   0.219,
	"IQ2_XXS": 0.258,
	"IQ2_XS":  0.289,
	"IQ2_S":   0.338,
	"IQ2_M":   0.369,
	"IQ3_XXS": 0.381,
	"IQ3_S":   0.428,
	"IQ3_M":   0.469,
	"IQ4_XS":  0.531,
	"IQ4_NL":  0.563,

	// Non‑GGUF (keys uppercase for ToUpper lookup)
	"BF16":           2.0,
	"F16":            2.0,
	"FP16":           2.0,
	"F32":            4.0,
	"FP32":           4.0,
	"AWQ-4BIT":       0.563,
	"AWQ-8BIT":       1.0,
	"GPTQ-INT2":      0.313,
	"GPTQ-INT4":      0.563,
	"GPTQ-INT8":      1.0,
	"AUTOROUND-4BIT": 0.563,
	"AUTOROUND-8BIT": 1.0,
}

// EstimateSpeed returns (tokSOut, confidenceLabel) for a model on the given GPU.
// It tries exact benchmarks first, then a memory-bandwidth/compute aware heuristic.
func EstimateSpeed(m *model.Model, gpu *hardware.GPU, db *BenchmarkDB) (float64, string) {
	if gpu == nil {
		return 0, ConfHeuristic
	}

	canonical := strings.ToLower(gpu.CanonicalName)
	gpuName := strings.ToLower(gpu.Name)
	hfID := strings.ToLower(m.Name)

	// ---- Tier A: exact benchmark match by GPU name ----
	if db != nil {
		if tok, ok := lookupMedian(db.byNameHfID, canonical, hfID); ok {
			return tok, ConfBenchmark
		}
		if tok, ok := lookupMedian(db.byNameHfID, gpuName, hfID); ok {
			return tok, ConfBenchmark
		}
	}

	// ---- Tier B: roofline heuristic ----
	bytesPerParam := BytesPerParam(m.Quantization)
	totalModelSzGB := float64(m.ParametersRaw) / 1e9 * bytesPerParam

	// Mixture of Experts logic: Decode streaming and compute only scales on active parameters
	activeParams := m.ActiveParameters
	if activeParams <= 0 {
		activeParams = m.ParametersRaw // Fallback for dense models
	}
	activeModelSzGB := float64(activeParams) / 1e9 * bytesPerParam

	memoryBound := 0.0
	if gpu.Bandwidth > 0 && activeModelSzGB > 0 {
		efficiency := GetMemoryEfficiency(gpu.Architecture)
		memoryBound = (gpu.Bandwidth * efficiency) / activeModelSzGB
	}
	computeBound := computeBoundEstimate(gpu, int(activeParams))

	var tokS float64
	switch {
	case memoryBound > 0 && computeBound > 0:
		tokS = min(memoryBound, computeBound)
	case memoryBound > 0:
		tokS = memoryBound
	default:
		tokS = computeBound
	}

	// Apply VRAM spill penalty if total weights + context overhead exceeds GPU VRAM
	if gpu.VRAMGB > 0 && (totalModelSzGB+vramOverheadGB) > gpu.VRAMGB {
		tokS *= 0.2
	}

	if tokS <= 0 {
		return 0, ConfHeuristic
	}
	return math.Round(tokS*10) / 10, ConfHeuristic
}

// ModelWeightSizeGB returns the on-disk weight size of a model in GB.
// This is the theoretical minimum VRAM needed for the weights alone (no KV cache, no CUDA overhead).
func ModelWeightSizeGB(m *model.Model) float64 {
	if m == nil || m.ParametersRaw <= 0 {
		return 0
	}
	return float64(m.ParametersRaw) / 1e9 * BytesPerParam(m.Quantization)
}

// ModelVRAMRequirement returns the honest VRAM requirement for a model: the
// larger of the curated MinVRAMGB and the computed weight size. This prevents
// cases where the catalog's MinVRAMGB is set lower than the actual weight
// footprint, causing misleading "Recommended" fit for models that spill.
func ModelVRAMRequirement(m *model.Model) float64 {
	if m == nil {
		return 0
	}
	weightGB := ModelWeightSizeGB(m)
	return max(m.MinVRAMGB, weightGB)
}

// BytesPerParam returns bytes-per-parameter for a quantization tag.
func BytesPerParam(quant string) float64 {
	if b, ok := quantBytesPerParam[strings.ToUpper(quant)]; ok {
		return b
	}
	return 2.0 // Fallback to FP16
}

// flopsPerParamPerToken is the cost of a single-token forward pass: roughly
// two FLOPs per parameter (one multiply-add).
const flopsPerParamPerToken = 2.0

// computeBoundEstimate caps tok/s by raw arithmetic throughput.
func computeBoundEstimate(gpu *hardware.GPU, activeParams int) float64 {
	if gpu.TFLOPS <= 0 || activeParams <= 0 {
		return 0
	}
	flopsPerToken := flopsPerParamPerToken * float64(activeParams)
	utilization := GetComputeEfficiency(gpu.Architecture)
	peakFLOPs := gpu.TFLOPS * 1e12 * utilization
	return peakFLOPs / flopsPerToken
}

func lookupMedian(index map[string][]BenchmarkRow, gpuName, hfID string) (float64, bool) {
	key := gpuName + "|" + hfID
	rows, ok := index[key]
	if !ok || len(rows) == 0 {
		return 0, false
	}
	vals := make([]float64, 0, len(rows))
	for _, r := range rows {
		if r.TokSOut > 0 {
			vals = append(vals, r.TokSOut)
		}
	}
	if len(vals) == 0 {
		return 0, false
	}
	return median(vals), true
}

func median(vals []float64) float64 {
	n := len(vals)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, vals)
	slices.Sort(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
