package predictor

import (
	"encoding/json"
	"math"
	"os"
	"strings"

	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
	"github.com/cezaryt5/Can_I_Run_IT/internal/model"
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

// LoadBenchmarks — internal/predictor/estimate.go:81
// Called from: cmd/ciri/main.go:51; predictor_test.go:198,214
// Reads benchmark_cache.json and builds two indices:
//   - byNameHfID: keyed by "gpuName|hfId" for exact GPU matches
//   - byArchHfID: keyed by "architecture|hfId" for arch-family fallback
// Skips rows where TokSOut is nil.
func LoadBenchmarks(path string, gpuDB []hardware.GPU) (*BenchmarkDB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

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

// ByNameHfID — internal/predictor/estimate.go:147
// Called from: benchmarks.go:31 (in newBenchmarksModel)
// Returns the raw byNameHfID benchmark index map for external querying
// (used by the benchmarks TUI screen).
func (db *BenchmarkDB) ByNameHfID() map[string][]BenchmarkRow {
	return db.byNameHfID
}

// extractGPUName — internal/predictor/estimate.go:153
// Called from: estimate.go:105 (in LoadBenchmarks); predictor_test.go:106
// Strips the VRAM suffix from a benchmark preset name for indexing.
// E.g. "RTX 5090 (32 GB)" → "RTX 5090", "Apple M4 Max (128 GB)" → "Apple M4 Max"
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

// memoryEfficiency — no GPU hits 100% of spec-sheet bandwidth in practice.
const memoryEfficiency = 0.75

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

// EstimateSpeed — internal/predictor/estimate.go:263
// Called from: predictor.go:61 (in Predict); predictor_test.go:226,250,270
// Three-tier speed estimation for a model on a GPU:
//   Tier A: exact benchmark match by GPU name → returns median tok/s (ConfBenchmark)
//   Tier B: architecture-family scaling → returns scaled median (ConfEstimate)
//   Tier C: roofline heuristic (memory-bound vs compute-bound) → returns min (ConfHeuristic)
// Applies a 20 % spill penalty if model does not fit in VRAM.
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
		memoryBound = (gpu.Bandwidth * memoryEfficiency) / modelSzGB
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

// BytesPerParam — internal/predictor/estimate.go:325
// Called from: estimate.go:295,354 (in EstimateSpeed and applySpillPenalty); results.go:539 (as predictor.BytesPerParam)
// Returns bytes-per-parameter for a given quantization tag (e.g., "Q4_K_M" → 0.625).
// Falls back to 2.0 (FP16) if the quantization is not in the lookup table.
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

// computeBoundEstimate — internal/predictor/estimate.go:343
// Called from: estimate.go:302 (in EstimateSpeed)
// Caps tok/s by raw arithmetic throughput: (peak FLOP/s × utilization) /
// (FLOPs per token = 2 × parameters). Returns 0 if TFLOPS or params are
// unknown.
func computeBoundEstimate(gpu *hardware.GPU, m *model.Model) float64 {
	if gpu.TFLOPS <= 0 || m.ParametersRaw <= 0 {
		return 0
	}
	flopsPerToken := flopsPerParamPerToken * float64(m.ParametersRaw)
	peakFLOPs := gpu.TFLOPS * 1e12 * modelFLOPUtilization
	return peakFLOPs / flopsPerToken
}

// applySpillPenalty — internal/predictor/estimate.go:353
// Called from: estimate.go:286 (in EstimateSpeed)
// Reduces estimated tok/s by 80 % when the model's size (params × bytes-per-param)
// exceeds available GPU VRAM, simulating the penalty of CPU-offloaded inference.
func applySpillPenalty(tokS float64, gpu *hardware.GPU, m *model.Model) float64 {
	bytesPerParam := BytesPerParam(m.Quantization)
	modelSzGB := float64(m.ParametersRaw) / 1e9 * bytesPerParam
	if gpu.VRAMGB > 0 && modelSzGB > gpu.VRAMGB {
		return tokS * 0.2
	}
	return tokS
}

// lookupMedian — internal/predictor/estimate.go:362
// Called from: estimate.go:274,277 (in EstimateSpeed)
// Looks up benchmark rows by "gpuName|hfID" key and returns the median tok/s.
// Returns (0, false) if no rows are found or all have zero tok/s.
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

// archScaledEstimate — internal/predictor/estimate.go:380
// Called from: estimate.go:285 (in EstimateSpeed)
// Looks up benchmark rows by architecture family key ("arch|hfID") and returns
// the median tok/s. The targetTFLOPs parameter is reserved for future scaling
// but currently the raw median is returned.
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

// median — internal/predictor/estimate.go:409
// Called from: estimate.go:377,397 (in lookupMedian and archScaledEstimate); predictor_test.go:156
// Computes the median of a float64 slice. Returns 0 for an empty slice.
// Sorts a copy of the input to avoid mutation.
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

// sortFloat64 — internal/predictor/estimate.go:423
// Called from: estimate.go:416 (in median)
// In-place insertion sort for float64 slices. Used by median() for small
// benchmark result sets.
func sortFloat64(a []float64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// archFactor — internal/predictor/estimate.go:431
// Called from: predictor_test.go:133
// Returns the tok/s-per-TFLOP factor for a given architecture string.
// Matches against the archFactors map, trying exact match then substring
// containment. Defaults to 1.0 if no match is found.
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
