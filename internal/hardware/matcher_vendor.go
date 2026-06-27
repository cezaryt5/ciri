package hardware

import (
	"context"
	"strings"
)

// VendorAPIMatcher queries vendor-specific CLI tools (nvidia-smi, rocm-smi,
// glxinfo) for the GPU marketing name and resolves it against the database.
type VendorAPIMatcher struct{}

// Detect — internal/hardware/matcher_vendor.go:12
// Called from: detection.go:147 (via GPUMatcher interface)
// Detects the GPU by querying vendor CLI tools (nvidia-smi for NVIDIA,
// rocm-smi for AMD, system_profiler for macOS) and resolving the returned
// name against the GPU database via resolveByName.
func (m *VendorAPIMatcher) Detect(ctx context.Context, gpuDB []GPU) (*GPU, float64, error) {
	pci := detectPCI()
	name := detectVendorName(ctx, pci)
	if name == "" {
		return nil, 0, nil
	}
	return resolveByName(gpuDB, name, 0.95)
}

// resolveByName — internal/hardware/matcher_vendor.go:23
// Called from: matcher_vendor.go:18 (in VendorAPIMatcher.Detect); matcher_pci_test.go:101
// Tries progressively less-reliable name lookups against the GPU DB with
// decreasing confidence: exact marketing name → alias → canonical → fuzzy.
// Base confidence (0.95) is decremented at each fallback stage.
func resolveByName(db []GPU, name string, baseConf float64) (*GPU, float64, error) {
	// Exact marketing name match (case-insensitive).
	if gpu := findGPUByName(db, name); gpu != nil {
		return gpu, baseConf, nil
	}

	// Alias match (e.g., "RTX 4090" → canonical).
	if gpu := findGPUByAlias(db, name); gpu != nil {
		return gpu, baseConf - 0.03, nil
	}

	// Canonical name match (e.g., "rtx 4090").
	normalized := NormalizeGPUName(name)
	if gpu := findGPUByCanonicalName(db, normalized); gpu != nil {
		return gpu, baseConf - 0.05, nil
	}

	// Fuzzy match — last resort within vendor API.
	candidates := fuzzyFindGPUs(db, normalized)
	if len(candidates) == 1 {
		return candidates[0], baseConf - 0.08, nil
	}
	if len(candidates) > 1 {
		return candidates[0], baseConf - 0.15, nil
	}

	return nil, 0, nil
}

// findGPUByName — internal/hardware/matcher_vendor.go:53
// Called from: matcher_ghw.go:17; matcher_vendor.go:25 (in resolveByName)
// Case-insensitive exact match of the marketing name against the GPU database.
func findGPUByName(db []GPU, name string) *GPU {
	lower := strings.ToLower(strings.TrimSpace(name))
	for i := range db {
		if strings.ToLower(db[i].Name) == lower {
			return &db[i]
		}
	}
	return nil
}

// findGPUByAlias — internal/hardware/matcher_vendor.go:64
// Called from: matcher_ghw.go:22; matcher_vendor.go:30 (in resolveByName)
// Case-insensitive match against each GPU's pre-computed alias list
// (stripped vendor prefixes from deriveAliases).
func findGPUByAlias(db []GPU, name string) *GPU {
	lower := strings.ToLower(strings.TrimSpace(name))
	for i := range db {
		for _, alias := range db[i].Aliases {
			if strings.ToLower(alias) == lower {
				return &db[i]
			}
		}
	}
	return nil
}

// findGPUByCanonicalName — internal/hardware/matcher_vendor.go:77
// Called from: matcher_ghw.go:28; matcher_vendor.go:36 (in resolveByName)
// Matches a GPU by its canonical (normalized) name. Both the input and
// stored canonical names are lowercased for comparison.
func findGPUByCanonicalName(db []GPU, canonical string) *GPU {
	lower := strings.ToLower(strings.TrimSpace(canonical))
	for i := range db {
		if strings.ToLower(db[i].CanonicalName) == lower {
			return &db[i]
		}
	}
	return nil
}

// fuzzyFindGPUs — internal/hardware/matcher_vendor.go:88
// Called from: matcher_ghw.go:33; matcher_vendor.go:41 (in resolveByName)
// Returns all GPUs whose canonical or marketing name contains the query
// string (case-insensitive). Prefix-matching entries are moved to the front
// for preferred ordering.
func fuzzyFindGPUs(db []GPU, query string) []*GPU {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}

	var results []*GPU
	for i := range db {
		g := &db[i]
		if strings.Contains(strings.ToLower(g.CanonicalName), q) ||
			strings.Contains(strings.ToLower(g.Name), q) {
			results = append(results, g)
		}
	}

	// Prefer matches where the canonical name starts with the query.
	for i := 0; i < len(results); i++ {
		if strings.HasPrefix(strings.ToLower(results[i].CanonicalName), q) {
			results[0], results[i] = results[i], results[0]
			break
		}
	}

	return results
}

// tokenOverlapScore — internal/hardware/matcher_vendor.go:116
// Called from: matcher_ghw.go:40,42; matcher_pci_test.go:139
// Counts the number of shared tokens between two GPU names (both are
// tokenized via tokenizeGPUName). Tokens shorter than 2 characters are
// ignored. Used to rank fuzzy matches by lexical similarity.
func tokenOverlapScore(a, b string) int {
	aTokens := tokenizeGPUName(a)
	bTokens := tokenizeGPUName(b)

	if len(aTokens) < 2 || len(bTokens) < 2 {
		return 0
	}

	bSet := make(map[string]bool)
	for _, token := range bTokens {
		bSet[token] = true
	}

	score := 0
	for _, token := range aTokens {
		if bSet[token] {
			score++
		}
	}

	return score
}

// tokenizeGPUName — internal/hardware/matcher_vendor.go:139
// Called from: matcher_vendor.go:117-118 (in tokenOverlapScore)
// Normalizes a GPU name and splits it into tokens (whitespace-separated parts
// with length ≥ 2). Used by tokenOverlapScore for fuzzy matching.
func tokenizeGPUName(name string) []string {
	normalized := NormalizeGPUName(name)
	parts := strings.Fields(normalized)
	var tokens []string
	for _, p := range parts {
		if len(p) >= 2 {
			tokens = append(tokens, p)
		}
	}
	return tokens
}
