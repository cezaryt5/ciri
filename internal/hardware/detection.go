package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jaypipes/ghw"
)

// ---- Types ----

type Specs struct {
	CpuModel      string
	CpuCores      int64
	RamTotalGB    float64
	RamAvailGB    float64
	RamTotalBytes uint64 // Retained for precise offloading math
	RamAvailBytes uint64

	RawGPUName string
	GPUID      int64
	VRAMTotal  float64

	HasOllama   bool
	HasLlamaCPP bool
}

type GPU struct {
	ID            int64
	Name          string
	CanonicalName string
	VRAMGB        float64
	Bandwidth     float64
	TFLOPS        float64
	IsLaptop      bool
	Architecture  string

	// PCI identifiers.
	VendorID        string
	DeviceIDs       []string
	LaptopDeviceIDs map[string]bool
	Aliases         []string
}

type DetectionStatus int

const (
	GPUExact DetectionStatus = iota
	GPUUnverified
	GPUNotFound
)

type DetectionResult struct {
	Specs  Specs
	GPU    *GPU
	Status DetectionStatus
}

type GPUMatcher interface {
	Detect(ctx context.Context, gpuDB []GPU) (*GPU, float64, error)
}

type PCIInfo struct {
	VendorID string
	DeviceID string
	SysfsDir string
}

// ---- Main Entry Point ----

// GetSpecs — internal/hardware/detection.go:75
// Called from: cmd/ciri/main.go:36
// Entry point for hardware detection. Orchestrates CPU, RAM, tool, and GPU
// detection in order. Returns a DetectionResult containing the detected Specs,
// best-guess GPU, and a DetectionStatus indicating confidence level.
func GetSpecs(gpuDB []GPU) (DetectionResult, error) {
	var res DetectionResult

	if err := res.Specs.DetectCPU(); err != nil {
		return res, fmt.Errorf("CPU detection failed: %w", err)
	}
	if err := res.Specs.DetectRAM(); err != nil {
		return res, fmt.Errorf("RAM detection failed: %w", err)
	}

	res.Specs.DetectOllamaCpp()

	status, bestGuess := res.Specs.DetectGPU(gpuDB)
	res.Status = status
	res.GPU = bestGuess
	return res, nil
}

// ---- CPU / RAM / Tools Detection ----

// DetectCPU — internal/hardware/detection.go:95
// Called from: detection.go:78 (via GetSpecs)
// Uses ghw to detect CPU model name and total core count. Falls back to
// "Unknown" on error.
func (s *Specs) DetectCPU() error {
	cpu, err := ghw.CPU()
	if err != nil || cpu == nil {
		s.CpuModel = "Unknown"
		return fmt.Errorf("failed to read CPU info, check permissions")
	}
	s.CpuCores = int64(cpu.TotalCores)
	if len(cpu.Processors) > 0 {
		s.CpuModel = cpu.Processors[0].Model
	} else {
		s.CpuModel = "Unknown"
	}
	return nil
}

// DetectRAM — internal/hardware/detection.go:110
// Called from: detection.go:81 (via GetSpecs)
// Uses ghw to detect total and usable physical RAM. Stores both float (GB)
// and uint64 (bytes) representations for display and offloading math.
func (s *Specs) DetectRAM() error {
	mem, err := ghw.Memory()
	if err != nil || mem == nil {
		return fmt.Errorf("failed to read memory info, check permissions")
	}

	s.RamTotalBytes = uint64(mem.TotalPhysicalBytes)
	s.RamAvailBytes = uint64(mem.TotalUsableBytes)

	// Keep floats for display, but keep bytes for math.
	s.RamTotalGB = float64(mem.TotalPhysicalBytes) / (1024 * 1024 * 1024)
	s.RamAvailGB = float64(mem.TotalUsableBytes) / (1024 * 1024 * 1024)
	return nil
}

// DetectOllamaCpp — internal/hardware/detection.go:125
// Called from: detection.go:85 (via GetSpecs)
// Checks whether the Ollama CLI and llama.cpp-related binaries (llama.cpp,
// llama-cli, llama-server) are on PATH.
func (s *Specs) DetectOllamaCpp() {
	s.HasOllama = execLookPath("ollama") != ""
	// Removed the "main" binary check.
	s.HasLlamaCPP = execLookPath("llama.cpp") != "" ||
		execLookPath("llama-cli") != "" ||
		execLookPath("llama-server") != ""
}

// ---- GPU Detection Cascade ----

// DetectGPU — internal/hardware/detection.go:135
// Called from: detection.go:87 (via GetSpecs); detection_test.go:407,430
// Cascades through three GPU matching strategies in order of confidence:
// 1. PCIMatcher (PCI vendor/device ID — highest)
// 2. VendorAPIMatcher (nvidia-smi / rocm-smi name — medium)
// 3. GHWFuzzyMatcher (ghw library + fuzzy name — lowest)
// Returns immediately if any strategy reaches ≥0.95 confidence.
func (s *Specs) DetectGPU(gpuDB []GPU) (DetectionStatus, *GPU) {
	// Requires implementations in your matcher_*.go files
	strategies := []GPUMatcher{
		&PCIMatcher{},
		&VendorAPIMatcher{},
		&GHWFuzzyMatcher{},
	}

	var highestConfidence float64
	var bestGuess *GPU

	for _, strategy := range strategies {
		gpu, confidence, err := strategy.Detect(context.Background(), gpuDB)
		if err != nil || gpu == nil {
			continue
		}
		if confidence >= 0.95 {
			s.GPUID = gpu.ID
			s.RawGPUName = gpu.Name
			s.VRAMTotal = gpu.VRAMGB
			return GPUExact, gpu
		}
		if confidence > highestConfidence {
			bestGuess = gpu
			highestConfidence = confidence
		}
	}

	if bestGuess != nil {
		s.GPUID = bestGuess.ID
		s.RawGPUName = bestGuess.Name
		s.VRAMTotal = bestGuess.VRAMGB
		return GPUUnverified, bestGuess
	}

	s.RawGPUName = "Unknown GPU"
	return GPUNotFound, nil
}

// ---- GPU Database Loading ----

type gpuJSON struct {
	Name         string             `json:"name"`
	Category     *string            `json:"category"`
	PCIVendorID  *string            `json:"pci_vendor_id"`
	PCIDeviceID  *string            `json:"pci_device_id"`
	PCIDeviceIDs []string           `json:"pci_device_ids"`
	PCIVariants  map[string]variant `json:"pci_variants"`
	VRAMGB       *float64           `json:"vram_gb"`
	Bandwidth    *float64           `json:"memory_bandwidth_gbps"`
	TFLOPS       *float64           `json:"tflops"`
	Architecture *string            `json:"architecture"`
}

type variant struct {
	IDs         []string `json:"ids"`
	Description string   `json:"description"`
}

// LoadGPUDB — internal/hardware/detection.go:194
// Called from: cmd/ciri/main.go:29; detection_test.go:47,97,108,141,181
// Reads and parses gpus.json into a []GPU slice. For each entry it
// normalizes the name via NormalizeGPUName, collects PCI IDs (deduplicated),
// detects laptop variants from name/PCI variants, and generates aliases via
// deriveAliases.
func LoadGPUDB(path string) ([]GPU, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw []gpuJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	gpus := make([]GPU, 0, len(raw))
	for i, r := range raw {
		g := GPU{
			ID:              int64(i + 1),
			Name:            r.Name,
			CanonicalName:   NormalizeGPUName(r.Name),
			LaptopDeviceIDs: make(map[string]bool),
		}
		if r.VRAMGB != nil {
			g.VRAMGB = *r.VRAMGB
		}
		if r.Bandwidth != nil {
			g.Bandwidth = *r.Bandwidth
		}
		if r.TFLOPS != nil {
			g.TFLOPS = *r.TFLOPS
		}
		if r.Architecture != nil {
			g.Architecture = *r.Architecture
		}
		if r.PCIVendorID != nil {
			g.VendorID = strings.ToLower(*r.PCIVendorID)
		}

		seen := make(map[string]bool)
		if r.PCIDeviceID != nil {
			id := strings.ToLower(*r.PCIDeviceID)
			if !seen[id] {
				g.DeviceIDs = append(g.DeviceIDs, id)
				seen[id] = true
			}
		}
		for _, id := range r.PCIDeviceIDs {
			id = strings.ToLower(id)
			if !seen[id] {
				g.DeviceIDs = append(g.DeviceIDs, id)
				seen[id] = true
			}
		}

		g.IsLaptop = strings.Contains(strings.ToLower(r.Name), "laptop") ||
			strings.Contains(strings.ToLower(r.Name), "mobile")
		for variantKey, v := range r.PCIVariants {
			key := strings.ToLower(variantKey)
			if key == "mobile" || key == "max_q" {
				for _, id := range v.IDs {
					g.LaptopDeviceIDs[strings.ToLower(id)] = true
				}
				g.IsLaptop = g.IsLaptop || len(v.IDs) > 0
			}
		}

		g.Aliases = deriveAliases(r.Name)
		gpus = append(gpus, g)
	}
	return gpus, nil
}

// deriveAliases — internal/hardware/detection.go:263
// Called from: detection.go:257 (in LoadGPUDB); detection_test.go:358
// Strips vendor prefixes ("nvidia geforce rtx ", "amd radeon rx ", etc.)
// from a GPU name to produce shorter alias strings used for fuzzy matching.
// Stops after the first matching prefix.
func deriveAliases(name string) []string {
	lower := strings.ToLower(strings.TrimSpace(name))
	var aliases []string

	// Ordered by longest prefix first to avoid partial stripping
	vendorPrefixes := []string{
		"nvidia geforce rtx ", "nvidia geforce gtx ", "nvidia geforce ", "nvidia rtx ", "nvidia ", "geforce ",
		"amd radeon rx ", "amd radeon pro ", "amd radeon ", "amd instinct ", "amd ", "radeon ",
		"intel arc ", "intel ", "arc ",
	}

	for _, prefix := range vendorPrefixes {
		if strings.HasPrefix(lower, prefix) {
			alias := strings.TrimSpace(strings.TrimPrefix(lower, prefix))
			if alias != "" && alias != lower {
				aliases = append(aliases, alias)
			}
			break // Only strip the primary vendor prefix once
		}
	}

	return aliases
}

// ---- GPU Database Lookup Helpers ----

// findGPUsByPCI — internal/hardware/detection.go:289
// Called from: matcher_pci.go:15; detection_test.go:201,218,230,247; matcher_pci_test.go:62
// Searches the GPU database for entries matching a given PCI vendor ID and
// device ID combination. Returns all matching GPUs (there may be multiple
// variants sharing the same PCI ID, e.g. desktop and laptop SKUs).
func findGPUsByPCI(db []GPU, vendorID, deviceID string) []*GPU {
	var matches []*GPU
	for i := range db {
		g := &db[i]
		if g.VendorID != vendorID {
			continue
		}
		for _, did := range g.DeviceIDs {
			if did == deviceID {
				matches = append(matches, g)
				break
			}
		}
	}
	return matches
}

// pickBestPCIMatch — internal/hardware/detection.go:306
// Called from: matcher_pci.go:21; detection_test.go:261,268,280,285,298,311
// When multiple GPUs share the same PCI ID, picks the best match:
//   - If VRAM was detected, selects the match with VRAM closest to detected.
//   - Otherwise prefers desktop variants over laptop.
//   - Falls back to the first match.
func pickBestPCIMatch(matches []*GPU, detectedVRAMGB float64) *GPU {
	if len(matches) == 0 {
		return nil
	}
	if len(matches) == 1 {
		return matches[0]
	}

	if detectedVRAMGB > 0 {
		best := matches[0]
		bestDiff := abs(detectedVRAMGB - best.VRAMGB)
		for _, g := range matches[1:] {
			diff := abs(detectedVRAMGB - g.VRAMGB)
			if diff < bestDiff {
				best = g
				bestDiff = diff
			}
		}
		return best
	}

	for _, g := range matches {
		if !g.IsLaptop {
			return g
		}
	}
	return matches[0]
}

// abs — internal/hardware/detection.go:335
// Called from: detection.go:316,318 (in pickBestPCIMatch); detection_test.go:389
// Float absolute value helper.
func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
