package hardware

import (
	"context"
	"strings"
)

// VendorAPIMatcher queries vendor-specific CLI tools (nvidia-smi, rocm-smi,
// glxinfo) for the GPU marketing name and resolves it against the database.
type VendorAPIMatcher struct{}

func (m *VendorAPIMatcher) Detect(ctx context.Context, gpuDB []GPU) (*GPU, float64, error) {
	pci := detectPCI(ctx)
	name := detectVendorName(ctx, pci)
	if name == "" {
		return nil, 0, nil
	}
	return resolveByName(gpuDB, name, 0.95)
}

// resolveByName tries progressively less-reliable name lookups and
// lowers the confidence at each step.
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

// findGPUByName searches for an exact marketing name match (case-insensitive).
func findGPUByName(db []GPU, name string) *GPU {
	lower := strings.ToLower(strings.TrimSpace(name))
	for i := range db {
		if strings.ToLower(db[i].Name) == lower {
			return &db[i]
		}
	}
	return nil
}

// findGPUByAlias checks every GPU's alias list for a match.
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

// findGPUByCanonicalName matches the normalized form of a GPU name.
func findGPUByCanonicalName(db []GPU, canonical string) *GPU {
	lower := strings.ToLower(strings.TrimSpace(canonical))
	for i := range db {
		if strings.ToLower(db[i].CanonicalName) == lower {
			return &db[i]
		}
	}
	return nil
}

// fuzzyFindGPUs returns all GPUs whose canonical name contains the query string.
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

// tokenOverlapScore counts shared tokens between two normalized GPU names.
// Tokens shorter than 2 characters are ignored (e.g., "a" is too ambiguous).
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
