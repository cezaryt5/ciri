package predictor

import (
	"encoding/json"
	"math"
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
	byArchHfID map[string][]BenchmarkRow // key: "architecture|hfId"
}

// LoadBenchmarks reads benchmark_cache.json and builds indices.
func LoadBenchmarks(data []byte, gpuDB []hardware.GPU) (*BenchmarkDB, error) {
	var cache benchmarkCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}

	db := &BenchmarkDB{
		byNameHfID: make(map[string][]BenchmarkRow),
		byArchHfID: make(map[string][]BenchmarkRow),
	}

	// Build gpu name → architecture lookup
	nameArch := make(map[string]string)
	for i := range gpuDB {
		nameArch[strings.ToLower(gpuDB[i].Name)] = strings.ToLower(gpuDB[i].Architecture)
		nameArch[strings.ToLower(gpuDB[i].CanonicalName)] = strings.ToLower(gpuDB[i].Architecture)
	}

	for presetName, preset := range cache.Presets {
		presetGPUName := extractGPUName(presetName)

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

			// Index by preset GPU name (e.g., "RTX 3090")
			key := strings.ToLower(presetGPUName + "|" + row.HfID)
			db.byNameHfID[key] = append(db.byNameHfID[key], row)

			// Index by architecture (for fallback estimation)
			arch := nameArch[strings.ToLower(presetGPUName)]
			if arch != "" {
				archKey := arch + "|" + strings.ToLower(row.HfID)
				db.byArchHfID[archKey] = append(db.byArchHfID[archKey], row)
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

// confidence labels.
const (
	ConfBenchmark = "Benchmark"
	ConfEstimate  = "Estimate"
	ConfHeuristic = "Heuristic"
)

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

// GetMemoryEfficiency returns the bandwidth scaling factor for a given
// GPU architecture. Unrecognized architectures get a conservative baseline.
func GetMemoryEfficiency(arch string) float64 {
	arch = strings.ToLower(strings.TrimSpace(arch))
	if eff, ok := memoryEfficiencyByArch[arch]; ok {
		return eff
	}
	// Fuzzy fallback: check if the arch string contains a known family name.
	for family, eff := range memoryEfficiencyByArch {
		if strings.Contains(arch, family) || strings.Contains(family, arch) {
			return eff
		}
	}
	// System RAM / DDR fallback — no dedicated VRAM to optimize.
	if strings.Contains(arch, "ddr") || strings.Contains(arch, "system") {
		return 0.45
	}
	return 0.60 // Conservative baseline for unrecognized architectures.
}

// quantBytesPerParam maps exact quantization tags to bytes per parameter.
// Derivation: bits-per-weight / 8. GGUF bitrates are not flat; e.g. Q4_0
// is 4.50 bpw (0.5625 B), Q4_K_M is 4.80 bpw (0.60 B). Using rounded 4-bit
// arithmetic would under‑estimate VRAM by 3+ GB for a 32B model.
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
	"Q4_K_S":  0.573,
	"Q4_K_M":  0.625,
	"Q4_K_L":  0.625,
	"Q5_0":    0.688,
	"Q5_1":    0.688,
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

	// Non‑GGUF
	"F16":            2.0,
	"FP16":           2.0,
	"F32":            4.0,
	"FP32":           4.0,
	"AWQ-4bit":       0.563,
	"AWQ-8bit":       1.0,
	"GPTQ-Int2":      0.313,
	"GPTQ-Int4":      0.563,
	"GPTQ-Int8":      1.0,
	"AutoRound-4bit": 0.563,
	"AutoRound-8bit": 1.0,
}

// archFactors maps architecture families to approximate tok/s per TFLOP.
// Roughly calibrated — these are conservative defaults that get superseded
// by real benchmark data when available.
var archFactors = map[string]float64{
	// NVIDIA
	"ada lovelace": 1.8,
	"ampere":       1.5,
	"turing":       1.2,
	"hopper":       2.5,
	"volta":        1.2,
	"pascal":       1.0,
	"maxwell":      0.7,
	"kepler":       0.5,
	// AMD
	"rdna 3":  1.3,
	"rdna 2":  1.0,
	"rdna 1":  0.8,
	"cdna 3":  2.2,
	"cdna 2":  1.8,
	"cdna 1":  1.5,
	"vega":    0.7,
	"polaris": 0.5,
	"navi":    0.9,
	// Apple
	"apple m4": 2.5,
	"apple m3": 2.0,
	"apple m2": 1.5,
	"apple m1": 1.0,
	// Intel
	"alchemist":  0.8,
	"battlemage": 1.0,
	"gaudi 3":    2.0,
	"gaudi 2":    1.5,
}

// EstimateSpeed returns (tokSOut, confidenceLabel) for a model on the given
// GPU. It tries benchmarks first, then architecture-family scaling, then a
// memory-bandwidth-aware heuristic.
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

	// ---- Tier B: architecture-family match ----
	arch := strings.ToLower(gpu.Architecture)
	if arch != "" && db != nil {
		if scaled, ok := archScaledEstimate(arch, hfID, gpu.TFLOPS, db); ok {
			return applySpillPenalty(scaled, gpu, m), ConfEstimate
		}
	}

	// ---- Tier C: roofline heuristic ----
	// Single-stream (batch=1) decode streams the entire weight set from
	// memory once per token, so throughput is dominated by memory bandwidth.
	// Compute only binds for very large models on bandwidth-rich GPUs, so we
	// take the lower of the memory-bound and compute-bound estimates.
	bytesPerParam := BytesPerParam(m.Quantization)
	modelSzGB := float64(m.ParametersRaw) / 1e9 * bytesPerParam

	memoryBound := 0.0
	if gpu.Bandwidth > 0 && modelSzGB > 0 {
		efficiency := GetMemoryEfficiency(gpu.Architecture)
		memoryBound = (gpu.Bandwidth * efficiency) / modelSzGB
	}
	computeBound := computeBoundEstimate(gpu, m)

	var tokS float64
	switch {
	case memoryBound > 0 && computeBound > 0:
		tokS = min(memoryBound, computeBound)
	case memoryBound > 0:
		tokS = memoryBound
	default:
		tokS = computeBound
	}

	if gpu.VRAMGB > 0 && modelSzGB > gpu.VRAMGB {
		tokS *= 0.2
	}

	if tokS <= 0 {
		return 0, ConfHeuristic
	}
	return math.Round(tokS*10) / 10, ConfHeuristic
}

// BytesPerParam returns bytes-per-parameter for a quantization tag.
func BytesPerParam(quant string) float64 {
	if b, ok := quantBytesPerParam[quant]; ok {
		return b
	}
	return 2.0
}

// flopsPerParamPerToken is the cost of a single-token forward pass: roughly
// two FLOPs per parameter (one multiply-add).
const flopsPerParamPerToken = 2.0

// modelFLOPUtilization is the fraction of a GPU's peak FP16 throughput that a
// single-stream decode realistically sustains. Decode is memory bound, so the
// effective compute utilization is low.
const modelFLOPUtilization = 0.20

// computeBoundEstimate caps tok/s by raw arithmetic throughput:
// (peak FLOP/s * utilization) / (FLOPs needed per token).
func computeBoundEstimate(gpu *hardware.GPU, m *model.Model) float64 {
	if gpu.TFLOPS <= 0 || m.ParametersRaw <= 0 {
		return 0
	}
	flopsPerToken := flopsPerParamPerToken * float64(m.ParametersRaw)
	peakFLOPs := gpu.TFLOPS * 1e12 * modelFLOPUtilization
	return peakFLOPs / flopsPerToken
}

// applySpillPenalty reduces tok/s when a model does not fit in VRAM.
func applySpillPenalty(tokS float64, gpu *hardware.GPU, m *model.Model) float64 {
	bytesPerParam := BytesPerParam(m.Quantization)
	modelSzGB := float64(m.ParametersRaw) / 1e9 * bytesPerParam
	if gpu.VRAMGB > 0 && modelSzGB > gpu.VRAMGB {
		return tokS * 0.2
	}
	return tokS
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

func archScaledEstimate(arch, hfID string, targetTFLOPs float64, db *BenchmarkDB) (float64, bool) {
	key := arch + "|" + hfID
	rows, ok := db.byArchHfID[key]
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

	med := median(vals)
	if targetTFLOPs <= 0 {
		return med, true
	}

	// We need the TFLOPs of the benchmarked GPU for scaling.
	// The architecture index doesn't store that — as a simple heuristic
	// we return the raw median. The caller can scale by TFLOPS ratio
	// externally if needed.
	return math.Round(med*10) / 10, true
}

func median(vals []float64) float64 {
	n := len(vals)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, vals)
	sortFloat64(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func sortFloat64(a []float64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func archFactor(arch string) float64 {
	arch = strings.ToLower(strings.TrimSpace(arch))
	if arch == "" {
		return 1.0
	}
	if f, ok := archFactors[arch]; ok {
		return f
	}
	for key, f := range archFactors {
		if strings.Contains(arch, key) || strings.Contains(key, arch) {
			return f
		}
	}
	return 1.0
}
