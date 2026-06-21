//go:build darwin

package hardware

import (
	"context"
	"strings"
	"time"

	"github.com/jaypipes/ghw"
)

// detectPCI — macOS uses IOKit, not sysfs. Return nil; fall back to
// detectVendorName (system_profiler) and GHW matchers.
func detectPCI(ctx context.Context) *PCIInfo {
	return nil
}

// detectVRAM — macOS can't reliably read VRAM without IOKit. Return 0.
func detectVRAM(ctx context.Context, target *PCIInfo) float64 {
	return 0
}

// detectVendorName — system_profiler gives the chipset model.
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

// glxinfoQuery — not available on macOS. Return "".
func glxinfoQuery(ctx context.Context) string {
	return ""
}

// detectRawGPUName — uses ghw as a fallback.
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
