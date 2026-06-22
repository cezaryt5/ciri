package predictor

import (
	"github.com/cezaryt5/ciri/internal/hardware"
	"github.com/cezaryt5/ciri/internal/model"
)

// FitStatus describes whether a model fits in available hardware resources.
type FitStatus int

const (
	Recommended FitStatus = iota
	Advanced
	TooHeavy
)

// String — internal/predictor/vram.go:17
// Called from: (implements fmt.Stringer)
// Returns a human-readable label for the FitStatus enum value.
func (s FitStatus) String() string {
	switch s {
	case Recommended:
		return "Recommended"
	case Advanced:
		return "Advanced"
	case TooHeavy:
		return "TooHeavy"
	default:
		return "Unknown"
	}
}

const (
	vramBufferFactor = 1.1  // 10 % headroom
	appleOSOverhead  = 4.0 // macOS typically reserves ~4 GB
)

// CheckFit — internal/predictor/vram.go:36
// Called from: predictor.go:56,91 (in Predict and CountByCategory); predictor_test.go:21,30,39,49,58,67,76,86
// Determines fit status for a model on given hardware:
//   - Apple Silicon: checks unified memory (minus 4 GB OS overhead) with 10% buffer
//   - dGPU: checks VRAM with 10% buffer → Recommended; else checks system RAM → Advanced; else TooHeavy
func CheckFit(m *model.Model, gpu *hardware.GPU, sysRAMAvailGB float64, isAppleSilicon bool) FitStatus {
	if isAppleSilicon {
		availableUnified := sysRAMAvailGB - appleOSOverhead
		if availableUnified < 0 {
			availableUnified = 0
		}
		if m.MinVRAMGB*vramBufferFactor <= availableUnified {
			return Recommended
		}
		return TooHeavy
	}

	vramHeadroom := 0.0
	if gpu != nil && gpu.VRAMGB > 0 {
		vramHeadroom = gpu.VRAMGB
	}
	if m.MinVRAMGB*vramBufferFactor <= vramHeadroom {
		return Recommended
	}
	if m.MinRAMGB <= sysRAMAvailGB {
		return Advanced
	}
	return TooHeavy
}
