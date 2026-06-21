//go:build windows

package hardware

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// detectPCI — internal/hardware/tools_windows.go:15
// Called from: matcher_pci.go:10, matcher_vendor.go:13
// Queries Windows PnP device IDs via PowerShell (modern) or wmic (fallback)
// for the primary display adapter. Extracts PCI vendor/device IDs from the
// PNPDeviceID string.
func detectPCI(ctx context.Context) *PCIInfo {
	// Try PowerShell first (modern Windows).
	out, err := execWithTimeout(ctx, 5*time.Second,
		"powershell", "-Command", `Get-PnpDevice -Class Display | ForEach-Object { $_.InstanceId }`)
	if err == nil {
		if pci := parseWindowsPnP(out); pci != nil {
			return pci
		}
	}

	// Fallback to wmic (deprecated but works on older builds).
	out, err = execWithTimeout(ctx, 3*time.Second,
		"wmic", "path", "win32_VideoController", "get", "PNPDeviceID")
	if err != nil {
		return nil
	}
	return parseWindowsPnP(out)
}

// parseWindowsPnP — internal/hardware/tools_windows.go:34
// Called from: tools_windows.go:20,31 (in detectPCI)
// Parses a Windows PnP device ID string (e.g. "PCI\VEN_10DE&DEV_2684") using
// regex to extract the vendor and device IDs.
func parseWindowsPnP(out []byte) *PCIInfo {
	re := regexp.MustCompile(`VEN_([0-9A-Fa-f]{4})&DEV_([0-9A-Fa-f]{4})`)
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "PCI\\") && !strings.Contains(line, "VEN_") {
			continue
		}
		matches := re.FindStringSubmatch(line)
		if len(matches) == 3 {
			return &PCIInfo{
				VendorID: strings.ToLower(matches[1]),
				DeviceID: strings.ToLower(matches[2]),
			}
		}
	}
	return nil
}

// detectVRAM — internal/hardware/tools_windows.go:55
// Called from: matcher_pci.go:20
// Windows VRAM detection. For NVIDIA GPUs, uses nvidia-smi. Returns 0 for
// other vendors (the PCI matcher will fall back to desktop preference).
func detectVRAM(ctx context.Context, target *PCIInfo) float64 {
	if target == nil {
		return 0
	}

	// If NVIDIA and nvidia-smi is available, use it.
	if target.VendorID == "10de" {
		if out, err := execWithTimeout(ctx, 3*time.Second,
			"nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader,nounits"); err == nil {
			var vram float64
			if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &vram); err == nil && vram > 0 {
				return vram / 1024.0 // MiB → GiB
			}
		}
	}

	return 0
}

// detectVendorName — internal/hardware/tools_windows.go:84
// Called from: matcher_vendor.go:19
// Windows stub — returns "" so the PCI matcher handles identification
// based on vendor/device IDs alone.
func detectVendorName(ctx context.Context, target *PCIInfo) string {
	return ""
}

// detectRawGPUName — internal/hardware/tools_windows.go:91
// Called from: matcher_ghw.go:16
// Windows stub — returns "" so the GHW fuzzy matcher returns nil,
// relying on the PCI matcher instead.
func detectRawGPUName() string {
	return ""
}

// execLookPath — internal/hardware/tools_windows.go:97
// Called from: detection.go:142,144,145,146 (in DetectOllamaCpp)
// Returns the absolute path of an executable via exec.LookPath, or ""
// if not found on PATH.
func execLookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

// execWithTimeout — internal/hardware/tools_windows.go:108
// Called from: tools_windows.go:17,26,62
// Windows implementation of execWithTimeout. Runs a command with a context
// deadline and returns stdout bytes on success.
func execWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cmdCtx, name, args...).Output()
}
