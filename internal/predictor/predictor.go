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

// Predict returns all models in the given category with fit and speed estimates.
// Excludes TooHeavy models. Results are sorted: Recommended first, then by tok/s.
func (p *Predictor) Predict(category model.Category) []ModelPrediction {
	var results []ModelPrediction

	for i := range p.models {
		m := &p.models[i]
		if !hasCategory(m, category) {
			continue
		}

		fit := CheckFit(m, p.gpu, p.sysRAMAvail, p.isApple)
		if fit == TooHeavy {
			continue
		}

		tokPerSec, confidence := EstimateSpeed(m, p.gpu, p.benchmarks)

		results = append(results, ModelPrediction{
			Model:        m, // Pointing to original slice, no longer copying
			FitStatus:    fit,
			EstTokPerSec: tokPerSec,
			Confidence:   confidence,
		})
	}

	// Sorts Recommended (<) before Advanced. Breaks ties via Token Speed.
	// ASSUMES: Recommended iota < Advanced iota
	sort.Slice(results, func(i, j int) bool {
		if results[i].FitStatus != results[j].FitStatus {
			return results[i].FitStatus < results[j].FitStatus
		}
		return results[i].EstTokPerSec > results[j].EstTokPerSec
	})

	return results
}

// CountByCategory counts how many models per category are not TooHeavy.
// Used by the home screen menu to display "(N models fit)".
func (p *Predictor) CountByCategory() map[model.Category]int {
	counts := make(map[model.Category]int)

	// Optimized: O(Models) instead of O(Models * Categories)
	// We evaluate CheckFit exactly once per model now.
	for i := range p.models {
		m := &p.models[i]

		fit := CheckFit(m, p.gpu, p.sysRAMAvail, p.isApple)
		if fit == TooHeavy {
			continue
		}

		// If it fits, increment the count for every category it belongs to
		for _, cat := range m.Categories {
			counts[cat]++
		}
	}
	return counts
}

// hasCategory checks whether a model belongs to the given category.
func hasCategory(m *model.Model, want model.Category) bool {
	for _, c := range m.Categories {
		if c == want {
			return true
		}
	}
	return false
}
