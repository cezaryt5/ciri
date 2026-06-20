package hardware

import (
	"context"
	"database/sql"
	"math"
	"os/exec"
	"regexp"
	"strings"
	"time"

	db "github.com/cezaryt5/Can_I_Run_IT/internal/db/output"
	"github.com/cezaryt5/Can_I_Run_IT/internal/services"
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
	ID            int64
	Name          string
	CanonicalName string
	VRAMGB        float64
	Bandwidth     float64
	TFLOPS        float64
	IsLaptop      bool
	Architecture  string
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
	Detect(ctx context.Context) (*GPU, float64, error)
}

type PCIInfo struct {
	VendorID          string
	DeviceID          string
	SubsystemVendorID string
	SubsystemDeviceID string
}

// --- Global Normalization Variables ---

var (
	vendorPrefixes = []string{
		"nvidia corporation", "nvidia", "advanced micro devices", "amd",
		"intel corporation", "intel", "apple",
	}

	brandingReplacer = strings.NewReplacer(
		"geforce", "", "radeon (tm)", "radeon", "radeon(tm)", "radeon",
		"graphics", "", "laptop gpu", "laptop", "gpu", "",
		"mobile", "laptop", "with max-q design", "laptop", "max-q", "laptop",
	)

	driverVersionRe = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)
	multiSpaceRe    = regexp.MustCompile(`\s+`)
	specialCharRe   = regexp.MustCompile(`[^\w\s-]`)
)

// --- Main Entry ---

// GetSpecs returns the hardware specs, plus a boolean indicating if the
// application layer needs to prompt the user to resolve a GPU ambiguity.
func GetSpecs() DetectionResult {
	var res DetectionResult
	res.Specs.DetectCPU()
	res.Specs.DetectRAM()
	status, bestGuess := res.Specs.DetectGPU()
	res.Status = status
	res.GPU = bestGuess

	return res
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

func (s *Specs) DetectGPU(servic *services.HardwareService) (DetectionStatus, *GPU) {
	strategies := []GPUMatcher{
		&PCIMatcher{servic},
		&VendorAPIMatcher{},
		&GHWFuzzyMatcher{},
	}

	var highestConfidence float64
	var bestGuess *GPU

	for _, strategy := range strategies {
		gpu, confidence, err := strategy.Detect()
		if err != nil || gpu == nil {
			continue
		}

		if confidence >= 0.95 {
			s.GPUID = int64(gpu.ID)
			s.RawGPUName = gpu.Name
			s.VRAMTotal = gpu.VRAMGB
			return GPUExact, gpu
		}

		if confidence >= highestConfidence {
			bestGuess = gpu
			highestConfidence = confidence
		}

	}

	if bestGuess != nil {
		s.RawGPUName = bestGuess.Name
		return GPUUnverified, bestGuess
	}

	s.RawGPUName = "Unknown GPU"
	return GPUNotFound, nil
}

// --- String Normalization ---

func NormalizeGPUName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = driverVersionRe.ReplaceAllString(name, "")

	for _, prefix := range vendorPrefixes {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			name = strings.TrimSpace(name)
		}
	}

	name = brandingReplacer.Replace(name)
	name = specialCharRe.ReplaceAllString(name, "")
	name = multiSpaceRe.ReplaceAllString(name, " ")

	return strings.TrimSpace(name)
}

// --- GPU Database Conversion ---

func gpuFromDB(g db.Gpu) *GPU {
	return &GPU{
		ID:            g.ID,
		Name:          g.Name,
		CanonicalName: nullStr(g.CanonicalName),
		VRAMGB:        nullFloat(g.VramGb),
		Bandwidth:     nullFloat(g.MemoryBandwidthGbps),
		TFLOPS:        nullFloat(g.Fp16Tflops),
		IsLaptop:      strings.Contains(strings.ToLower(g.Name), "laptop"),
		Architecture:  nullStr(g.Architecture),
	}
}

// --------------- handle null values -----------------------------
func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
func nullFloat(nf sql.NullFloat64) float64 {
	if nf.Valid {
		return nf.Float64
	}
	return 0
}

func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// --- Command Execution Helper ---

func execWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cmdCtx, name, args...).Output()
}

// ------------ Matcher 1: PCI ID detection -------------------

type PCIMatcher struct {
	hardwareSRV *services.HardwareService
}

func (p *PCIMatcher) Detect(ctx context.Context) (*GPU, float64, error) {
	var pci *PCIInfo

	pci = detectPCILinux(ctx)

	if pci == nil {
		return nil, 0, nil
	}

	gpu, err := p.hardwareSRV.GetGPUByPCIID(ctx, db.GetGPUByPCIIDParams{
		VendorID:          pci.VendorID,
		DeviceID:          pci.DeviceID,
		SubsystemVendorID: toNullString(pci.SubsystemVendorID),
		SubsystemDeviceID: toNullString(pci.SubsystemDeviceID),
	})
	if err != nil {
		return nil, 0, nil
	}

	return gpuFromDB(gpu), 0.98, nil
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
