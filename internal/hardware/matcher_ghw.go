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
// Detect gets raw GPU names from ghw, then tries:
// exact name → alias → canonical → fuzzy substring.
// Returns a bare GPU with confidence 0.30 if no DB match is found.
func (m *GHWFuzzyMatcher) Detect(ctx context.Context, gpuDB []GPU) (*GPU, float64, error) {
	// 1. Handle the new slice signature
	rawNames := detectRawGPUNames()
	if len(rawNames) == 0 {
		return nil, 0, nil
	}

	// TEMPORARY: Just grab the first valid GPU until you refactor for multi-GPU
	rawName := rawNames[0]

	if rawName == "" || rawName == "None/Unsupported" || rawName == "Unknown" {
		return nil, 0, nil
	}

	// Try exact name match first.
	if gpu := findGPUByName(gpuDB, rawName); gpu != nil {
		return gpu, 0.90, nil
	}

	// Alias match.
	if gpu := findGPUByAlias(gpuDB, rawName); gpu != nil {
		return gpu, 0.85, nil
	}

	// Canonical name match.
	normalized := NormalizeGPUName(rawName)
	if gpu := findGPUByCanonicalName(gpuDB, normalized); gpu != nil {
		return gpu, 0.80, nil
	}

	// Fuzzy search by canonical name.
	candidates := fuzzyFindGPUs(gpuDB, normalized)
	if len(candidates) == 1 {
		return candidates[0], 0.70, nil
	}
	if len(candidates) > 1 {
		// fuzzyFindGPUs already sorted the best match to index 0 using
		// token overlap and length tie-breaking. We just grab it.
		// Confidence is lower (0.50) because multiple DB entries matched the query.
		return candidates[0], 0.50, nil
	}

	// DB lookup failed — return a bare GPU with just the detected name.
	// WARNING: This returns 0 VRAM. llmfit must be prepared to handle
	// hardware capability checks for a GPU with 0 recorded memory.
	return &GPU{
		Name:          rawName,
		CanonicalName: normalized,
	}, 0.30, nil
}
