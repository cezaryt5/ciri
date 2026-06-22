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
	Model        model.Model
	FitStatus    FitStatus
	EstTokPerSec float64
	Confidence   string
}

// NewPredictor — internal/predictor/predictor.go:30
// Called from: cmd/ciri/main.go:57; predictor_test.go:291,316,343,364,393
// Creates a Predictor bound to the detected GPU, available system RAM, model
// catalog, and benchmark database. Automatically detects Apple Silicon by
// checking GPU name, architecture, and vendor ID (106b).
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

// Predict — internal/predictor/predictor.go:47
// Called from: results.go:55 (in newResultsModel); predictor_test.go:293,318,326,345,399
// Returns all models in the given category with fit and speed estimates.
// Excludes TooHeavy models. Results are sorted: Recommended first (by tok/s
// descending), then Advanced.
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
			Model:        *m,
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

// CountByCategory — internal/predictor/predictor.go:83
// Called from: app.go:50 (in NewApp); predictor_test.go:366
// Counts how many models per category are not TooHeavy (Recommended +
// Advanced). Used by the home screen menu to display "(N models fit)".
func (p *Predictor) CountByCategory() map[model.Category]int {
	counts := make(map[model.Category]int)
	for _, cat := range model.AllCategories() {
		for i := range p.models {
			m := &p.models[i]
			if !hasCategory(m, cat) {
				continue
			}
			fit := CheckFit(m, p.gpu, p.sysRAMAvail, p.isApple)
			if fit != TooHeavy {
				counts[cat]++
			}
		}
	}
	return counts
}

// hasCategory — internal/predictor/predictor.go:100
// Called from: predictor.go:52,88 (in Predict and CountByCategory)
// Checks whether a model belongs to the given category by iterating its
// pre-computed Categories slice.
func hasCategory(m *model.Model, want model.Category) bool {
	for _, c := range m.Categories {
		if c == want {
			return true
		}
	}
	return false
}
