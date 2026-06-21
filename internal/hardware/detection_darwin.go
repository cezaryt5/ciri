//go:build darwin

package hardware

import (
	"context"
	"strings"
	"time"

	"github.com/jaypipes/ghw"
)

// detectPCI — internal/hardware/detection_darwin.go:15
// Called from: matcher_pci.go:10, matcher_vendor.go:13
// macOS has no sysfs — returns nil. Forces fallback to detectVendorName
// (system_profiler) and GHW matchers.
func detectPCI(ctx context.Context) *PCIInfo {
	return nil
}

// detectVRAM — internal/hardware/detection_darwin.go:20
// Called from: matcher_pci.go:20
// macOS stub — returns 0 since VRAM detection requires IOKit which is not
// available through this code path.
func detectVRAM(ctx context.Context, target *PCIInfo) float64 {
	return 0
}

// detectVendorName — internal/hardware/detection_darwin.go:25
// Called from: matcher_vendor.go:14
// Uses system_profiler SPDisplaysDataType to extract the "Chipset Model"
// line (e.g. "Apple M4 Max").
func detectVendorName(ctx context.Context, target *PCIInfo) string {
	if target == nil {
		return ""
	}
	out, err := execWithTimeout(ctx, 3*time.Second, "system_profiler", "SPDisplaysDataType")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Chipset Model:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

// glxinfoQuery — internal/hardware/detection_darwin.go:45
// Called from: (defined, not called in current code)
// macOS stub — glxinfo is not available. Returns "".
func glxinfoQuery(ctx context.Context) string {
	return ""
}

// detectRawGPUName — internal/hardware/detection_darwin.go:50
// Called from: matcher_ghw.go:11
// Uses ghw as a fallback on macOS. Returns the first GPU's product name,
// or "" if ghw fails.
func detectRawGPUName() string {
	gpuInfo, err := ghw.GPU()
	if err != nil || gpuInfo == nil || len(gpuInfo.GraphicsCards) == 0 {
		return ""
	}
	if gpuInfo.GraphicsCards[0].DeviceInfo != nil &&
		gpuInfo.GraphicsCards[0].DeviceInfo.Product != nil {
		return gpuInfo.GraphicsCards[0].DeviceInfo.Product.Name
	}
	return ""
}
