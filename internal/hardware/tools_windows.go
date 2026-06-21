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

// detectPCI queries Windows PnP device IDs for the primary display adapter.
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

// detectVRAM on Windows is not implemented without WMI or vendor-specific
// tooling. Return 0 — the PCI matcher will fall back to desktop preference.
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

// execWithTimeout on Windows.
func execWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cmdCtx, name, args...).Output()
}
