package predictor

import (
	"sort"
	"strings"

	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
)

// Predictor ties hardware, model catalog and benchmarks together and
// produces ranked predictions for a given category.
type Predictor struct {
	gpu         *hardware.GPU
	sysRAMAvail float64
	models      []model.Model
	benchmarks  *BenchmarkDB
	isApple     bool
}

// ModelPrediction is a single model's fit and speed assessment.
type ModelPrediction struct {
	Model        *model.Model // Changed to pointer to prevent heavy memory duplication
	FitStatus    FitStatus
	EstTokPerSec float64
	Confidence   string
}

// NewPredictor creates a Predictor bound to the detected hardware.
// Note: Single GPU bottleneck remains here. Tech debt for future multi-GPU scaling.
func NewPredictor(gpu *hardware.GPU, sysRAMAvailGB float64, models []model.Model, benchmarks *BenchmarkDB) *Predictor {
	p := &Predictor{
		gpu:         gpu,
		sysRAMAvail: sysRAMAvailGB,
		models:      models,
		benchmarks:  benchmarks,
	}

	if gpu != nil {
		p.isApple = strings.Contains(strings.ToLower(gpu.Name), "apple") ||
			strings.Contains(strings.ToLower(gpu.Architecture), "apple") ||
			gpu.VendorID == "106b"
	}
	return p
}

// PredictAll returns fit/speed estimates for every model in the catalog
// (including TooHeavy). Used by the explore-all screen. Sorted:
// Recommended first, then by tok/s.
func (p *Predictor) PredictAll() []ModelPrediction {
	var results []ModelPrediction
	for i := range p.models {
		m := &p.models[i]
		if len(m.Quants) > 0 {
			// Pick the best-quality quant that still fits. This keeps the
			// first catalog honest: a model with a Q2 that fits is not shown
			// as "slow" only because its default Q4_K_M does not.
			var best ModelPrediction
			bestBits := -1
			for _, q := range m.Quants {
				pred := p.PredictQuant(m, q, q.PreferredSource())
				if best.Model == nil || pred.FitStatus < best.FitStatus ||
					(pred.FitStatus == best.FitStatus && q.Bits > bestBits) {
					best = pred
					bestBits = q.Bits
				}
			}
			results = append(results, best)
			continue
		}
		fit := CheckFit(m, p.gpu, p.sysRAMAvail, p.isApple)
		tokPerSec, confidence := EstimateSpeed(m, p.gpu, p.benchmarks)
		results = append(results, ModelPrediction{
			Model:        m,
			FitStatus:    fit,
			EstTokPerSec: tokPerSec,
			Confidence:   confidence,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].FitStatus != results[j].FitStatus {
			return results[i].FitStatus < results[j].FitStatus
		}
		return results[i].EstTokPerSec > results[j].EstTokPerSec
	})
	return results
}

// PredictQuant evaluates one quant/source combination for the detail screen.
// It uses the exact file size when available and falls back to the model's
// params × bytes-per-param estimate.
func (p *Predictor) PredictQuant(m *model.Model, q model.Quant, source *model.QuantSource) ModelPrediction {
	mm := *m
	mm.Quantization = q.Label
	mm.WeightsBytes = q.WeightsBytes
	mm.BenchmarkIDs = q.SourceRepos()
	if source != nil {
		if source.WeightsBytes > 0 {
			mm.WeightsBytes = source.WeightsBytes
		}
		mm.BenchmarkIDs = []string{source.Repo}
	}
	mm.MinVRAMGB = float64(mm.WeightsBytes) / 1e9
	mm.MinRAMGB = mm.MinVRAMGB*1.15 + 2
	if mm.MinRAMGB < 1 {
		mm.MinRAMGB = 1
	}
	mm.RecommendedRAMGB = mm.MinRAMGB + 4
	fit := CheckFit(&mm, p.gpu, p.sysRAMAvail, p.isApple)
	tokPerSec, confidence := EstimateSpeed(&mm, p.gpu, p.benchmarks)
	return ModelPrediction{
		Model:        &mm,
		FitStatus:    fit,
		EstTokPerSec: tokPerSec,
		Confidence:   confidence,
	}
}

// BenchmarkIDs returns the variant repo IDs used to look up measurements.
func (p *Predictor) BenchmarkIDs(m *model.Model) []string {
	if len(m.BenchmarkIDs) > 0 {
		return m.BenchmarkIDs
	}
	if strings.Contains(m.Name, "/") {
		return []string{m.Name}
	}
	return nil
}
