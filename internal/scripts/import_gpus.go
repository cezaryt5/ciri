package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

/*
	func escapeSQL(s string) string {
		return strings.ReplaceAll(s, "'", "''")
	}
*/
func readCSV(filename string) ([][]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", filename, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filename, err)
	}
	return records, nil
}

func detectVendor(name string) string {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "nvidia") {
		return "NVIDIA"
	}
	if strings.HasPrefix(lower, "amd") {
		return "AMD"
	}
	if strings.HasPrefix(lower, "intel") {
		return "Intel"
	}
	if strings.HasPrefix(lower, "apple") {
		return "Apple"
	}
	return "Unknown"
}

func parseIntOrNull(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return "NULL"
	}
	if _, err := strconv.Atoi(s); err != nil {
		return "NULL"
	}
	return s
}

func parseFloatOrNull(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "0.0" {
		return "NULL"
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return "NULL"
	}
	return s
}

func parseVramGB(vram string) string {
	vram = strings.TrimSpace(vram)
	lower := strings.ToLower(vram)
	if lower == "shared" || lower == "system shared" || lower == "" {
		return "0"
	}
	return vram
}

// NormalizeGPUName creates a canonical_name for exact normalized matching.
// Lowercased, vendor prefix removed, special chars removed, GB normalized.
func NormalizeGPUName(name string, vendor string) string {
	n := strings.ToLower(strings.TrimSpace(name))

	// Remove vendor prefix.
	vendorPrefixes := []string{
		"nvidia ", "amd ", "intel ", "apple ",
	}
	for _, prefix := range vendorPrefixes {
		if strings.HasPrefix(n, prefix) {
			n = strings.TrimPrefix(n, prefix)
			break
		}
	}

	// Remove branding terms.
	replacer := strings.NewReplacer(
		"geforce ", "",
		"radeon ", "",
		"instinct ", "",
		"tesla ", "",
		"arc ", "",
		"gaudi ", "",
		"®", "",
		"™", "",
	)

	n = replacer.Replace(n)

	// Normalize "GB" variants.
	n = strings.ReplaceAll(n, " gb", "gb")

	// Remove special characters, keep letters, digits, and spaces.
	var b strings.Builder
	for _, r := range n {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		}
	}
	n = b.String()

	// Collapse multiple spaces.
	fields := strings.Fields(n)
	n = strings.Join(fields, " ")

	return n
}

// generateAliasesForGPU creates common aliases for a single GPU name.
func generateAliasesForGPU(name string, vendor string) []string {
	seen := map[string]bool{}
	var aliases []string

	add := func(a string) {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			return
		}
		seen[a] = true
		aliases = append(aliases, a)
	}

	// 1. Canonical name.
	add(name)

	baseline := name

	// 2. Remove vendor prefix.
	for _, prefix := range []string{"NVIDIA ", "AMD ", "Intel ", "Apple "} {
		if strings.HasPrefix(baseline, prefix) {
			noVendor := strings.TrimPrefix(baseline, prefix)
			add(noVendor)
			baseline = noVendor
			break
		}
	}

	// 3. Remove "GeForce " / "Radeon ".
	withoutBrand := baseline
	for _, brand := range []string{"GeForce ", "Radeon ", "Instinct ", "Tesla ", "Arc ", "Gaudi "} {
		if strings.HasPrefix(withoutBrand, brand) {
			noBrand := strings.TrimPrefix(withoutBrand, brand)
			add(noBrand)
			withoutBrand = noBrand
			break
		}
	}

	// 4. Normalize "GB" spacing.
	gbVariants := []string{baseline, withoutBrand}
	for _, v := range gbVariants {
		if strings.Contains(v, " GB") {
			add(strings.ReplaceAll(v, " GB", "GB"))
		}
		if strings.Contains(v, "GB") && !strings.Contains(v, " GB") {
			add(strings.ReplaceAll(v, "GB", " GB"))
		}
	}

	return aliases
}

// generateGPUSeedSQL generates INSERT INTO gpus for both discrete and Apple GPUs.
func generateGPUSeedSQL(gpuRecords, appleRecords [][]string) string {
	var sb strings.Builder

	sb.WriteString("DELETE FROM gpus;\n")
	sb.WriteString("INSERT INTO gpus (\n")
	sb.WriteString("    vendor, name, canonical_name,\n")
	sb.WriteString("    vram_gb, memory_type, memory_bus, pcie,\n")
	sb.WriteString("    shading_units, tmus, rops,\n")
	sb.WriteString("    release_date, architecture,\n")
	sb.WriteString("    memory_bandwidth_gbps, fp16_tflops, int8_tops, tdp_watts\n")
	sb.WriteString(") VALUES\n")

	rowCount := 0

	// Discrete GPUs from GPUs.csv.
	for i, row := range gpuRecords {
		if i == 0 {
			continue
		}

		name := strings.TrimSpace(row[0])
		vendor := detectVendor(name)
		canonical := NormalizeGPUName(name, vendor)

		vramGB := parseVramGB(row[1])
		memType := strings.TrimSpace(row[2])
		memBus := parseIntOrNull(row[3])
		pcie := strings.TrimSpace(row[4])
		shadingUnits := parseIntOrNull(row[5])
		tmus := parseIntOrNull(row[6])
		rops := parseIntOrNull(row[7])
		releaseDate := strings.TrimSpace(row[8])
		architecture := strings.TrimSpace(row[9])
		memBW := parseFloatOrNull(row[10])
		fp16 := parseFloatOrNull(row[11])
		int8 := parseFloatOrNull(row[12])
		tdp := parseIntOrNull(row[13])

		if pcie == "" {
			pcie = "NULL"
		} else {
			pcie = "'" + escapeSQL(pcie) + "'"
		}

		if memType == "" {
			memType = "NULL"
		} else {
			memType = "'" + escapeSQL(memType) + "'"
		}

		if releaseDate == "" {
			releaseDate = "NULL"
		} else {
			releaseDate = "'" + escapeSQL(releaseDate) + "'"
		}

		if architecture == "" {
			architecture = "NULL"
		} else {
			architecture = "'" + escapeSQL(architecture) + "'"
		}

		if canonical == "" {
			canonical = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
		}

		comma := ","
		if i == len(gpuRecords)-1 && len(appleRecords) <= 1 {
			comma = ";"
		}

		sb.WriteString(fmt.Sprintf(
			"  ('%s', '%s', '%s', %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)%s\n",
			escapeSQL(vendor),
			escapeSQL(name),
			escapeSQL(canonical),
			vramGB,
			memType,
			memBus,
			pcie,
			shadingUnits,
			tmus,
			rops,
			releaseDate,
			architecture,
			memBW,
			fp16,
			int8,
			tdp,
			comma,
		))

		rowCount++
	}

	// Apple Silicon GPUs from Apple_Silicon_GPUs.csv.
	for i, row := range appleRecords {
		if i == 0 {
			continue
		}

		name := strings.TrimSpace(row[0])
		vendor := "Apple"
		canonical := NormalizeGPUName(name, vendor)

		// Apple CSV columns:
		// Name, GPU_Cores, Memory_GB, Memory_Type, Memory_Bandwidth_GBps,
		// GPU_TFLOPS, Neural_Engine_TOPS, TDP_Watts, Release_Date, Architecture

		// GPU_Cores is Apple-specific; we set shading_units to gpu_cores as a proxy.
		gpuCores := row[1]
		memoryGB := strings.TrimSpace(row[2])
		memType := strings.TrimSpace(row[3])
		memBW := parseFloatOrNull(row[4])
		gpuTF := parseFloatOrNull(row[5])
		// Neural_Engine_TOPS is at index 6, stored in int8_tops as a proxy.
		neuralEngine := parseFloatOrNull(row[6])
		tdp := parseIntOrNull(row[7])
		releaseDate := strings.TrimSpace(row[8])
		architecture := strings.TrimSpace(row[9])

		if memType == "" {
			memType = "NULL"
		} else {
			memType = "'" + escapeSQL(memType) + "'"
		}

		if releaseDate == "" {
			releaseDate = "NULL"
		} else {
			releaseDate = "'" + escapeSQL(releaseDate) + "'"
		}

		if architecture == "" {
			architecture = "NULL"
		} else {
			architecture = "'" + escapeSQL(architecture) + "'"
		}

		if canonical == "" {
			canonical = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
		}

		comma := ","
		if i == len(appleRecords)-1 {
			comma = ";"
		}

		sb.WriteString(fmt.Sprintf(
			"  ('%s', '%s', '%s', %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)%s\n",
			escapeSQL(vendor),
			escapeSQL(name),
			escapeSQL(canonical),
			memoryGB, // vram_gb — unified memory for Apple
			memType,  // memory_type
			"NULL",   // memory_bus
			"NULL",   // pcie
			gpuCores, // shading_units — using as GPU core count proxy
			"NULL",   // tmus
			"NULL",   // rops
			releaseDate,
			architecture,
			memBW,        // memory_bandwidth_gbps
			gpuTF,        // fp16_tflops
			neuralEngine, // int8_tops — using as neural_engine proxy
			tdp,
			comma,
		))

		rowCount++
	}

	return sb.String()
}

// generateAliasesSeedSQL generates INSERT INTO gpu_aliases for all GPUs.
func generateAliasesSeedSQL(gpuRecords, appleRecords [][]string) string {
	var sb strings.Builder

	sb.WriteString("DELETE FROM gpu_aliases;\n")

	for i, row := range gpuRecords {
		if i == 0 {
			continue
		}

		name := strings.TrimSpace(row[0])
		_ = i

		sb.WriteString(generateAliasInsertsForName(name) + "\n")
	}

	for i, row := range appleRecords {
		if i == 0 {
			continue
		}

		name := strings.TrimSpace(row[0])
		_ = i

		sb.WriteString(generateAliasInsertsForName(name) + "\n")
	}

	return sb.String()
}

func generateAliasInsertsForName(name string) string {
	var sb strings.Builder
	vendor := detectVendor(name)
	aliases := generateAliasesForGPU(name, vendor)

	escapedName := escapeSQL(name)

	for _, alias := range aliases {
		if alias == "" {
			continue
		}
		escapedAlias := escapeSQL(alias)
		sb.WriteString(fmt.Sprintf(
			"INSERT OR IGNORE INTO gpu_aliases (alias, gpu_id)\n"+
				"SELECT '%s', id FROM gpus WHERE name = '%s';\n",
			escapedAlias, escapedName,
		))
	}

	return sb.String()
}

/*
func main() {
	gpuRecords, err := readCSV("GPUs.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading GPUs.csv: %v\n", err)
		os.Exit(1)
	}

	appleRecords, err := readCSV("Apple_Silicon_GPUs.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading Apple_Silicon_GPUs.csv: %v\n", err)
		os.Exit(1)
	}

	gpuSeedSQL := generateGPUSeedSQL(gpuRecords, appleRecords)
	aliasesSeedSQL := generateAliasesSeedSQL(gpuRecords, appleRecords)

	os.MkdirAll("internal/db/migrations", 0755)

	if err := os.WriteFile("internal/db/migrations/002_seed_gpus.sql", []byte(gpuSeedSQL), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing 002_seed_gpus.sql: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile("internal/db/migrations/003_seed_gpu_aliases.sql", []byte(aliasesSeedSQL), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing 003_seed_gpu_aliases.sql: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated internal/db/migrations/002_seed_gpus.sql (%d GPU rows)\n", len(gpuRecords)+len(appleRecords)-2)
	fmt.Printf("Generated internal/db/migrations/003_seed_gpu_aliases.sql\n")
}
*/
