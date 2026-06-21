package hardware

import "context"

// GHWFuzzyMatcher uses the ghw library to get a raw GPU product name and
// attempts to match it against the database. This is the lowest-confidence
// strategy but always returns something if hardware is present.
type GHWFuzzyMatcher struct{}

func (m *GHWFuzzyMatcher) Detect(ctx context.Context, gpuDB []GPU) (*GPU, float64, error) {
	rawName := detectRawGPUName()
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
		// Use token overlap to pick the best fuzzy match.
		best := candidates[0]
		bestScore := tokenOverlapScore(rawName, best.Name)
		for _, g := range candidates[1:] {
			score := tokenOverlapScore(rawName, g.Name)
			if score > bestScore {
				best = g
				bestScore = score
			}
		}
		return best, 0.50, nil
	}

	// DB lookup failed — return a bare GPU with just the detected name.
	return &GPU{
		Name:          rawName,
		CanonicalName: normalized,
	}, 0.30, nil
}
