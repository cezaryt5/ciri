//go:build darwin

package hardware

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jaypipes/ghw"
)

// detectPCI returns nil on macOS — the platform has no sysfs.
func detectPCI() []*PCIInfo {
	return nil
}

// detectVRAM fetches total system memory via sysctl.
// On Apple Silicon, GPU memory is Unified Memory (shared with system RAM).
func detectVRAM(ctx context.Context, target *PCIInfo) float64 {
	out, err := execWithTimeout(ctx, 3*time.Second, "sysctl", "-n", "hw.memsize")
	if err == nil {
		var bytes uint64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &bytes); err == nil && bytes > 0 {
			return float64(bytes) / (1024 * 1024 * 1024) // Bytes to GiB
		}
	}
	return 0
}

// detectVendorNames queries system_profiler for the GPU model name.
func detectVendorNames(ctx context.Context) []string {
	// i intentionally ignore `pci` here because we know it's nil.
	// this is a macOS-specific implementation.

	out, err := execWithTimeout(ctx, 3*time.Second, "system_profiler", "SPDisplaysDataType")
	if err != nil {
		return nil
	}

	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Chipset Model:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				names = append(names, strings.TrimSpace(parts[1]))
			}
		}
	}
	return names
}

// detectRawGPUNames uses ghw as a fallback for GPU name detection.
func detectRawGPUNames() []string {
	gpuInfo, err := ghw.GPU()
	if err != nil || gpuInfo == nil || len(gpuInfo.GraphicsCards) == 0 {
		return nil
	}

	var names []string
	for _, card := range gpuInfo.GraphicsCards {
		if card != nil && card.DeviceInfo != nil && card.DeviceInfo.Product != nil {
			// Extracting the Apple SoC name (e.g., "Apple M2 Max")
			names = append(names, card.DeviceInfo.Product.Name)
		}
	}
	return names
}
