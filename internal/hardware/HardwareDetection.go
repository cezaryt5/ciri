package hardware

import (
	"math"
	"regexp"
	"strings"

	db "github.com/cezaryt5/Can_I_Run_IT/internal/db/output"
	"github.com/jaypipes/ghw"
	"github.com/shirou/gopsutil/v3/mem"
)

// --- Types & Interfaces ---

type Specs struct {
	CpuModel     string
	CpuCores     int64
	RamTotal     float64
	RamAvailable float64

	RawGPUName string
	GPUID      int64
	VRAMTotal  float64

	HasOllama   bool
	HasLlamaCPP bool
}

type GPU struct {
	ID            int
	Name          string
	CanonicalName string
	VRAMGB        float64
	Bandwidth     float64
	TFLOPS        float64
	IsLaptop      bool
	Architecture  string
}

type GPUMatcher interface {
	Detect() (*GPU, float64, error)
}

// --- Global Normalization Variables ---
// Compiled once at startup to avoid burning CPU cycles inside loops.

var (
	vendorPrefixes = []string{
		"nvidia corporation", "nvidia", "advanced micro devices", "amd",
		"intel corporation", "intel", "apple",
	}

	// strings.NewReplacer guarantees deterministic execution order, unlike a map.
	brandingReplacer = strings.NewReplacer(
		"geforce", "",
		"radeon (tm)", "radeon",
		"radeon(tm)", "radeon",
		"graphics", "",
		"laptop gpu", "laptop", // Must strictly precede "gpu"
		"gpu", "",
		"mobile", "laptop",
		"with max-q design", "laptop",
		"max-q", "laptop",
	)

	driverVersionRe = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)
	multiSpaceRe    = regexp.MustCompile(`\s+`)
	specialCharRe   = regexp.MustCompile(`[^\w\s-]`)
)

// --- Main Entry ---

// GetSpecs returns the hardware specs, plus a boolean indicating if the
// application layer needs to prompt the user to resolve a GPU ambiguity.
func GetSpecs() (Specs, bool, *GPU) {
	var s Specs
	s.DetectCPU()
	s.DetectRAM()
	needsPrompt, bestGuess := s.DetectGPU()
	// s.DetectBackends()

	return s, needsPrompt, bestGuess
}

// --- Hardware Detection ---

func (s *Specs) DetectCPU() {
	if cpu, err := ghw.CPU(); cpu != nil && err == nil {
		s.CpuCores = int64(cpu.TotalCores)

		if len(cpu.Processors) > 0 {
			s.CpuModel = cpu.Processors[0].Model
		} else {
			s.CpuModel = "Unknown"
		}
	}
}

func (s *Specs) DetectRAM() {
	v, err := mem.VirtualMemory()
	if err != nil {
		return
	}
	s.RamTotal = math.Round((float64(v.Total)/(1024*1024*1024))*10) / 10
	s.RamAvailable = math.Round((float64(v.Available)/(1024*1024*1024))*10) / 10
}

func (s *Specs) DetectGPU() (DetectionStatus, *GPU) {
	strategies := []GPUMatcher{
		&PCIMatcher{},
		&VendorAPIMatcher{},
		&GHWFuzzyMatcher{},
	}

	var highestConfidence float64
	var bestGPU float64
}

// ------------ pci detection -------------------

type PCIMatcher struct {
	q *db.Queries
}

// ------------- pci helpers ---------------------------

// ------------ vendor api detection -------------------

type VendorAPIMatcher struct {
	q *db.Queries
}

func (m *PCIMatcher) Detect() (*GPU, float64, error) {
	return nil, 0, nil
}

type VendorAPIMatcher struct{}

func (m *VendorAPIMatcher) Detect() (*GPU, float64, error) {
	return nil, 0, nil
}

type GHWFuzzyMatcher struct{}

func (m *GHWFuzzyMatcher) Detect() (*GPU, float64, error) {
	return nil, 0, nil
}
