package hardware

import "context"

// GHWFuzzyMatcher uses the ghw library to get a raw GPU product name and
// attempts to match it against the database. This is the lowest-confidence
// strategy but always returns something if hardware is present.
type GHWFuzzyMatcher struct{}

// Detect — internal/hardware/matcher_ghw.go:10
// Called from: detection.go:147 (via GPUMatcher interface)
// Lowest-confidence matcher. Gets raw GPU name from ghw, then tries:
// exact name → alias → canonical → fuzzy substring → token overlap scoring.
// Returns a bare GPU with confidence 0.30 even if no DB match is found.
func (m *GHWFuzzyMatcher) Detect(ctx context.Context, gpuDB []GPU) (*GPU, float64, error) {
	rawNames := detectRawGPUNames()
	if len(rawNames) == 0 {
		return nil, 0, nil
	}

	var firstValid string

	for _, rawName := range rawNames {
		if rawName == "" || rawName == "None/Unsupported" || rawName == "Unknown" {
			continue
		}
		if firstValid == "" {
			firstValid = rawName
		}

		if gpu := findGPUByName(gpuDB, rawName); gpu != nil {
			return gpu, 0.90, nil
		}
		if gpu := findGPUByAlias(gpuDB, rawName); gpu != nil {
			return gpu, 0.85, nil
		}

		normalized := NormalizeGPUName(rawName)
		if gpu := findGPUByCanonicalName(gpuDB, normalized); gpu != nil {
			return gpu, 0.80, nil
		}

		candidates := fuzzyFindGPUs(gpuDB, normalized)
		if len(candidates) == 1 {
			return candidates[0], 0.70, nil
		}
		if len(candidates) > 1 {
			return candidates[0], 0.50, nil
		}
	}

	if firstValid != "" {
		return &GPU{
			Name:          firstValid,
			CanonicalName: NormalizeGPUName(firstValid),
		}, 0.30, nil
	}
	return nil, 0, nil
}
