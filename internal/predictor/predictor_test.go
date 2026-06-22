package predictor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
	"github.com/cezaryt5/Can_I_Run_IT/internal/model"
)

// ---------------------------------------------------------------------------
// CheckFit
// ---------------------------------------------------------------------------

func TestCheckFit_Recommended(t *testing.T) {
	gpu := &hardware.GPU{VRAMGB: 12}
	m := &model.Model{MinVRAMGB: 8, MinRAMGB: 16}
	status := CheckFit(m, gpu, 30, false)
	if status != Recommended {
		t.Errorf("expected Recommended, got %v", status)
	}
}

func TestCheckFit_Advanced(t *testing.T) {
	gpu := &hardware.GPU{VRAMGB: 8}
	m := &model.Model{MinVRAMGB: 12, MinRAMGB: 16}
	status := CheckFit(m, gpu, 30, false)
	if status != Advanced {

		t.Errorf("expected Advanced, got %v", status)
	}
}

func TestCheckFit_TooHeavy(t *testing.T) {
	gpu := &hardware.GPU{VRAMGB: 8}
	m := &model.Model{MinVRAMGB: 24, MinRAMGB: 64}
	status := CheckFit(m, gpu, 30, false)
	if status != TooHeavy {
		t.Errorf("expected TooHeavy, got %v", status)
	}
}

func TestCheckFit_TightVRAM(t *testing.T) {
	// 10GB VRAM, model needs 9.5GB → 9.5*1.1 = 10.45 > 10 → Advanced
	gpu := &hardware.GPU{VRAMGB: 10}
	m := &model.Model{MinVRAMGB: 9.5, MinRAMGB: 16}
	status := CheckFit(m, gpu, 30, false)
	if status != Advanced {
		t.Errorf("expected Advanced (tight VRAM), got %v", status)
	}
}

func TestCheckFit_AppleSilicon(t *testing.T) {
	m := &model.Model{MinVRAMGB: 16}
	// available unified = 32 - 4 = 28 GB
	status := CheckFit(m, nil, 32, true)
	if status != Recommended {
		t.Errorf("expected Recommended on Apple Silicon, got %v", status)
	}
}

func TestCheckFit_AppleSiliconTooHeavy(t *testing.T) {
	m := &model.Model{MinVRAMGB: 32}
	// available unified = 24 - 4 = 20 GB, model needs 32*1.1 = 35.2
	status := CheckFit(m, nil, 24, true)
	if status != TooHeavy {
		t.Errorf("expected TooHeavy on Apple Silicon, got %v", status)
	}
}

func TestCheckFit_NilGPU(t *testing.T) {
	m := &model.Model{MinVRAMGB: 8, MinRAMGB: 16}
	// nil GPU → vramHeadroom = 0 → model needs VRAM → Advanced (CPU offload)
	status := CheckFit(m, nil, 30, false)
	if status != Advanced {
		t.Errorf("nil GPU, got %v, want Advanced (no VRAM, CPU offload)", status)
	}
}

func TestCheckFit_ZeroVRAMGPU(t *testing.T) {
	gpu := &hardware.GPU{VRAMGB: 0}
	m := &model.Model{MinVRAMGB: 8, MinRAMGB: 16}
	// zero VRAM → model needs VRAM → Advanced (CPU offload)
	status := CheckFit(m, gpu, 30, false)
	if status != Advanced {
		t.Errorf("zero VRAM GPU, got %v, want Advanced (no VRAM, CPU offload)", status)
	}
}

// ---------------------------------------------------------------------------
// extractGPUName
// ---------------------------------------------------------------------------

func TestExtractGPUName(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"RTX 5090 (32 GB)", "RTX 5090"},
		{"Apple M4 Max (128 GB)", "Apple M4 Max"},
		{"CPU Only", "CPU Only"},
		{" RX 7900 XT (20 GB) ", "RX 7900 XT"},
	}
	for _, tc := range tests {
		got := extractGPUName(tc.input)
		if got != tc.expected {
			t.Errorf("extractGPUName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// archFactor
// ---------------------------------------------------------------------------

func TestArchFactor(t *testing.T) {
	tests := []struct {
		arch     string
		expected float64
	}{
		{"Ada Lovelace", 1.8},
		{"AD102", 1.0}, // fallback
		{"ampere", 1.5},
		{"rdna 3", 1.3},
		{"apple m4", 2.5},
		{"apple m1", 1.0},

		{"nonexistent", 1.0},
		{"", 1.0},
	}
	for _, tc := range tests {
		got := archFactor(tc.arch)
		if got != tc.expected {
			t.Errorf("archFactor(%q) = %f, want %f", tc.arch, got, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// median / sortFloat64
// ---------------------------------------------------------------------------

func TestMedian(t *testing.T) {
	tests := []struct {
		vals     []float64
		expected float64
	}{
		{[]float64{1, 2, 3}, 2},
		{[]float64{1, 2, 3, 4}, 2.5},
		{[]float64{5}, 5},
		{[]float64{}, 0},
		{[]float64{10, 1, 5}, 5},
	}
	for _, tc := range tests {
		got := median(tc.vals)
		if got != tc.expected {
			t.Errorf("median(%v) = %f, want %f", tc.vals, got, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// LoadBenchmarks
// ---------------------------------------------------------------------------

func TestLoadBenchmarks_Valid(t *testing.T) {
	cache := benchmarkCacheFile{
		Presets: map[string]presetData{
			"RTX 5090 (32 GB)": {
				Rows: []benchmarkRowJSON{
					{
						Model:    benchmarkModelJSON{HfID: "qwen/qwen3-35b", DisplayName: "Qwen3-35B", Params: 35},
						Hardware: benchmarkHardwareJSON{HwClass: "DISCRETE_GPU", GpuName: strPtr("RTX 5090"), VRAMGB: f64Ptr(32)},
						Engine:   benchmarkEngineJSON{EngineName: "llama.cpp", Quantization: "Q4_K_M"},
						TokSOut:  f64Ptr(230.73),
					},
					{
						Model:    benchmarkModelJSON{HfID: "qwen/qwen3-35b", DisplayName: "Qwen3-35B", Params: 35},
						Hardware: benchmarkHardwareJSON{HwClass: "DISCRETE_GPU", GpuName: strPtr("RTX 5090"), VRAMGB: f64Ptr(32)},
						Engine:   benchmarkEngineJSON{EngineName: "vllm", Quantization: "NVFP4"},
						TokSOut:  f64Ptr(240.9),
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(cache)

	gpuDB := []hardware.GPU{
		{Name: "NVIDIA GeForce RTX 5090", CanonicalName: "5090", Architecture: "GB202"},
	}

	db, err := LoadBenchmarks(raw, gpuDB)
	if err != nil {
		t.Fatalf("LoadBenchmarks: %v", err)
	}

	key := strings.ToLower("RTX 5090" + "|" + "qwen/qwen3-35b")
	rows := db.byNameHfID[key]
	if len(rows) != 2 {
		t.Fatalf("expected 2 benchmark rows, got %d", len(rows))
	}
	if rows[0].TokSOut != 230.73 {
		t.Errorf("tok/s = %f, want 230.73", rows[0].TokSOut)
	}
}

func TestLoadBenchmarks_InvalidJSON(t *testing.T) {
	_, err := LoadBenchmarks([]byte("not json{{{"), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// EstimateSpeed
// ---------------------------------------------------------------------------

func TestEstimateSpeed_NoGPU(t *testing.T) {
	m := &model.Model{Name: "test/model", ParametersRaw: 7000000000}
	tok, conf := EstimateSpeed(m, nil, nil)
	if tok != 0 {
		t.Errorf("no GPU → tok/s should be 0, got %f", tok)
	}
	if conf != ConfHeuristic {
		t.Errorf("confidence = %q, want Heuristic", conf)
	}
}

func TestEstimateSpeed_BenchmarkHit(t *testing.T) {
	m := &model.Model{Name: "qwen/qwen3-35b", ParametersRaw: 35000000000}
	gpu := &hardware.GPU{
		Name: "NVIDIA GeForce RTX 5090", CanonicalName: "5090",
		TFLOPS: 209.5, Architecture: "GB202",
	}

	// Build an in-memory BenchmarkDB
	db := &BenchmarkDB{
		byNameHfID: map[string][]BenchmarkRow{
			"5090|qwen/qwen3-35b":                    {{TokSOut: 230.73}, {TokSOut: 240.90}},
			"nvidia geforce rtx 5090|qwen/qwen3-35b": {{TokSOut: 230.73}},
		},
	}

	tok, conf := EstimateSpeed(m, gpu, db)
	if conf != ConfBenchmark {
		t.Errorf("expected Benchmark confidence, got %q", conf)
	}
	// median of [230.73, 240.90] = 235.815
	if tok < 230 || tok > 241 {
		t.Errorf("tok/s = %f, expected ~235.8", tok)
	}
}

func TestEstimateSpeed_Heuristic(t *testing.T) {
	m := &model.Model{Name: "test/model", ParametersRaw: 7000000000, Quantization: "Q4_K_M"}
	gpu := &hardware.GPU{
		Name: "NVIDIA GeForce RTX 4090", CanonicalName: "4090",
		TFLOPS: 165.2, Bandwidth: 1008, VRAMGB: 24, Architecture: "AD102",
	}
	// No benchmarks → roofline heuristic. A 7B Q4_K_M model is ~4.375 GB, so
	// decode is memory bound: 1008 * 0.75 / 4.375 ≈ 173 tok/s (compute bound
	// is far higher and does not bind).

	tok, conf := EstimateSpeed(m, gpu, nil)
	if conf != ConfHeuristic {
		t.Errorf("expected Heuristic, got %q", conf)
	}
	if tok < 100 || tok > 250 {
		t.Errorf("tok/s = %f, expected reasonable heuristic", tok)
	}
}

// ---------------------------------------------------------------------------
// Predict
// ---------------------------------------------------------------------------

func TestPredict_SortsRecommendedFirst(t *testing.T) {
	models := []model.Model{
		{Name: "fast-model", MinVRAMGB: 4, MinRAMGB: 8, Categories: []model.Category{model.CategoryCoding},
			ParametersRaw: 4000000000, UseCase: "Code generation and completion"},
		{Name: "big-model", MinVRAMGB: 24, MinRAMGB: 32, Categories: []model.Category{model.CategoryCoding},
			ParametersRaw: 70000000000, UseCase: "Code generation and completion"},
	}
	gpu := &hardware.GPU{Name: "RTX 4090", VRAMGB: 16, TFLOPS: 165, Architecture: "AD102", CanonicalName: "4090"}
	p := NewPredictor(gpu, 64, models, nil)

	results := p.Predict(model.CategoryCoding)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].FitStatus != Recommended {
		t.Errorf("first result should be Recommended, got %v", results[0].FitStatus)
	}
	if results[1].FitStatus != Advanced {
		t.Errorf("second should be Advanced, got %v", results[1].FitStatus)
	}
	if results[0].EstTokPerSec < results[1].EstTokPerSec {
		t.Error("Recommended should have higher tok/s than Advanced")
	}
}

func TestPredict_FiltersByCategory(t *testing.T) {
	models := []model.Model{
		{Name: "coder", Categories: []model.Category{model.CategoryCoding},
			MinVRAMGB: 4, MinRAMGB: 8, UseCase: "Code generation and completion"},
		{Name: "chatter", Categories: []model.Category{model.CategoryChat},
			MinVRAMGB: 4, MinRAMGB: 8, UseCase: "Instruction following, chat"},
	}
	gpu := &hardware.GPU{Name: "RTX 4090", VRAMGB: 24, TFLOPS: 165, Architecture: "AD102", CanonicalName: "4090"}
	p := NewPredictor(gpu, 30, models, nil)

	codingResults := p.Predict(model.CategoryCoding)
	if len(codingResults) != 1 {
		t.Fatalf("expected 1 coding result, got %d", len(codingResults))
	}
	if codingResults[0].Model.Name != "coder" {
		t.Errorf("expected coder model, got %s", codingResults[0].Model.Name)
	}

	chatResults := p.Predict(model.CategoryChat)
	if len(chatResults) != 1 {
		t.Fatalf("expected 1 chat result, got %d", len(chatResults))
	}
	if chatResults[0].Model.Name != "chatter" {
		t.Errorf("expected chatter model, got %s", chatResults[0].Model.Name)
	}
}

func TestPredict_ExcludesTooHeavy(t *testing.T) {
	models := []model.Model{
		{Name: "huge", MinVRAMGB: 80, MinRAMGB: 100, Categories: []model.Category{model.CategoryGeneral},
			ParametersRaw: 400000000000, UseCase: "General purpose"},
		{Name: "small", MinVRAMGB: 2, MinRAMGB: 4, Categories: []model.Category{model.CategoryGeneral},
			ParametersRaw: 1000000000, UseCase: "General purpose"},
	}
	gpu := &hardware.GPU{Name: "RTX 3060", VRAMGB: 12, TFLOPS: 12.7, Architecture: "GA106", CanonicalName: "rtx 3060"}
	p := NewPredictor(gpu, 16, models, nil)

	results := p.Predict(model.CategoryGeneral)
	if len(results) != 1 {
		t.Fatalf("expected 1 result (small fits, huge excluded), got %d", len(results))
	}
	if results[0].Model.Name != "small" {
		t.Errorf("expected small model, got %s", results[0].Model.Name)
	}
}

func TestCountByCategory(t *testing.T) {
	models := []model.Model{
		{Name: "c1", Categories: []model.Category{model.CategoryCoding},
			MinVRAMGB: 4, MinRAMGB: 8, UseCase: "Code generation"},
		{Name: "c2", Categories: []model.Category{model.CategoryCoding},
			MinVRAMGB: 6, MinRAMGB: 8, UseCase: "Code generation"},
		{Name: "ch1", Categories: []model.Category{model.CategoryChat},
			MinVRAMGB: 4, MinRAMGB: 8, UseCase: "Instruction following"},
	}
	gpu := &hardware.GPU{Name: "RTX 4090", VRAMGB: 8, TFLOPS: 165, Architecture: "AD102", CanonicalName: "4090"}
	p := NewPredictor(gpu, 16, models, nil)

	counts := p.CountByCategory()
	// c1 fits (4GB), c2 fits (6GB) = 2 coding
	// ch1 fits (4GB) = 1 chat
	if counts[model.CategoryCoding] != 2 {
		t.Errorf("expected 2 coding models, got %d", counts[model.CategoryCoding])
	}
	if counts[model.CategoryChat] != 1 {
		t.Errorf("expected 1 chat model, got %d", counts[model.CategoryChat])
	}
	// Vision should be 0 since none are categorized as Vision
	if counts[model.CategoryVision] != 0 {
		t.Errorf("expected 0 vision models, got %d", counts[model.CategoryVision])
	}
}

func TestPredict_AppleSilicon(t *testing.T) {
	models := []model.Model{
		{Name: "apple-ok", MinVRAMGB: 20, MinRAMGB: 32, Categories: []model.Category{model.CategoryChat},
			ParametersRaw: 30000000000, UseCase: "Instruction following"},
		{Name: "apple-heavy", MinVRAMGB: 40, MinRAMGB: 64, Categories: []model.Category{model.CategoryChat},
			ParametersRaw: 70000000000, UseCase: "Instruction following"},
	}
	gpu := &hardware.GPU{
		Name: "Apple M4 Max (GPU)", Architecture: "Apple M4",
		VendorID: "106b", TFLOPS: 39, VRAMGB: 0,
		CanonicalName: "m4 max",
	}
	p := NewPredictor(gpu, 64, models, nil)

	if !p.isApple {
		t.Error("Apple Silicon not detected")
	}

	results := p.Predict(model.CategoryChat)
	// apple-ok: 20*1.1=22 <= (64-4)=60 → Recommended
	// apple-heavy: 40*1.1=44 <= 60 → Recommended
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.FitStatus != Recommended {
			t.Errorf("%s: expected Recommended, got %v", r.Model.Name, r.FitStatus)
		}
	}
}

// helpers

func strPtr(s string) *string   { return &s }
func f64Ptr(f float64) *float64 { return &f }
