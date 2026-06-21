package hardware

import (
	"regexp"
	"strings"
)

// ---- Global Normalization Variables ----

var (
	vendorPrefixes = []string{
		"nvidia corporation ", "nvidia geforce rtx ", "nvidia geforce gtx ",
		"nvidia geforce ", "nvidia rtx ", "nvidia ", "geforce ",
		"advanced micro devices ", "amd radeon rx ", "amd radeon pro ",
		"amd radeon ", "amd instinct ", "amd ", "radeon ",
		"intel corporation ", "intel arc ", "intel ", "arc ",
		"apple ",
	}

	driverVersionRe = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)
	multiSpaceRe    = regexp.MustCompile(`\s+`)
	specialCharRe   = regexp.MustCompile(`[^\w\s-]`)
)

// NormalizeGPUName converts a raw GPU marketing string into a compact,
// searchable canonical form. Examples:
//
//	"NVIDIA GeForce RTX 4090"              → "rtx 4090"
//	"AMD Radeon (TM) RX 7900 XTX"         → "rx 7900 xtx"
//	"Intel(R) Arc(TM) A770 Graphics"      → "arc a770"
//	"NVIDIA GeForce RTX 3080 Laptop GPU"  → "rtx 3080 laptop"
func NormalizeGPUName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))

	// 1. Strip driver version numbers.
	name = driverVersionRe.ReplaceAllString(name, "")

	// 2. Strip known vendor prefixes (longest first).
	for _, prefix := range vendorPrefixes {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			break
		}
	}
	name = strings.TrimSpace(name)

	// 3. Normalize trademark symbols and parentheticals.
	name = strings.NewReplacer(
		"(tm)", "", "(r)", "", "®", "", "™", "",
	).Replace(name)

	// 4. Collapse branding/marketing noise.
	// Order matters: replace longer phrases before shorter ones.
	name = strings.NewReplacer(
		"with max-q design", "",
		"laptop gpu", "laptop",
		"graphics", "",
		"gpu", "",
		"max-q", "",
		"mobile", "laptop",
	).Replace(name)

	// 5. Remove special characters (parentheses, commas, etc.).
	name = specialCharRe.ReplaceAllString(name, "")

	// 6. Collapse consecutive spaces.
	name = multiSpaceRe.ReplaceAllString(name, " ")

	return strings.TrimSpace(name)
}
