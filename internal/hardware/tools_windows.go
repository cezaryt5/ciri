//go:build windows

package hardware

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jaypipes/ghw"
)

// detectPCI queries Windows PnP device IDs via PowerShell or wmic.
// Now properly captures ALL GPUs, not just the first one.
func detectPCI() []*PCIInfo {
	// Try PowerShell first (modern Windows).
	out, err := execWithTimeout(context.Background(), 5*time.Second,
		"powershell", "-Command", `Get-PnpDevice -Class Display | ForEach-Object { $_.InstanceId }`)
	if err == nil {
		if pci := parseWindowsPnP(out); len(pci) > 0 {
			return pci
		}
	}

	// Fallback to wmic (deprecated but works on older builds).
	out, err = execWithTimeout(context.Background(), 3*time.Second,
		"wmic", "path", "win32_VideoController", "get", "PNPDeviceID")
	if err != nil {
		return nil
	}
	return parseWindowsPnP(out)
}

// parseWindowsPnP parses a Windows PnP device ID string and returns a
// deduplicated slice of PCIInfo entries.
func parseWindowsPnP(out []byte) []*PCIInfo {
	var results []*PCIInfo
	seen := map[string]bool{}
	re := regexp.MustCompile(`VEN_([0-9A-Fa-f]{4})&DEV_([0-9A-Fa-f]{4})`)
	lines := strings.Split(string(out), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "PCI\\") && !strings.Contains(line, "VEN_") {
			continue
		}
		matches := re.FindStringSubmatch(line)
		if len(matches) == 3 {
			key := strings.ToLower(matches[1]) + ":" + strings.ToLower(matches[2])
			if seen[key] {
				continue
			}
			seen[key] = true
			results = append(results, &PCIInfo{
				VendorID: strings.ToLower(matches[1]),
				DeviceID: strings.ToLower(matches[2]),
			})
		}
	}
	return results
}

// detectVRAM detects VRAM on Windows. For NVIDIA, queries all GPUs via
// nvidia-smi and matches by PCI device ID to get the correct card's VRAM.
// Falls back to WMIC AdapterRAM for AMD/Intel.
func detectVRAM(ctx context.Context, target *PCIInfo) float64 {
	if target == nil {
		return 0
	}

	// 1. NVIDIA: query all GPUs, match by PCI device ID
	if target.VendorID == "10de" {
		if out, err := execWithTimeout(ctx, 3*time.Second,
			"nvidia-smi", "--query-gpu=index,pci.device_id,memory.total", "--format=csv,noheader,nounits"); err == nil {

			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.Split(line, ",")
				if len(parts) < 3 {
					continue
				}
				// pci.device_id format: " 0x2684" → strip "0x" and spaces
				pciDeviceID := strings.TrimSpace(parts[1])
				pciDeviceID = strings.TrimPrefix(strings.ToLower(pciDeviceID), "0x")

				if pciDeviceID == target.DeviceID {
					var vram float64
					if _, err := fmt.Sscanf(strings.TrimSpace(parts[2]), "%f", &vram); err == nil && vram > 0 {
						return vram / 1024.0 // MiB to GiB
					}
				}
			}
		}
	}

	// 2. Generic Windows fallback for AMD/Intel.
	// Query AdapterRAM and PNPDeviceID together, match by device ID.
	out, err := execWithTimeout(ctx, 3*time.Second,
		"wmic", "path", "win32_VideoController", "get", "AdapterRAM,PNPDeviceID")
	if err == nil {
		re := regexp.MustCompile(`VEN_([0-9A-Fa-f]{4})&DEV_([0-9A-Fa-f]{4})`)
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "AdapterRAM") {
				continue
			}
			pnpMatch := re.FindStringSubmatch(line)
			if len(pnpMatch) != 3 {
				continue
			}
			vendor := strings.ToLower(pnpMatch[1])
			device := strings.ToLower(pnpMatch[2])
			if vendor != target.VendorID || device != target.DeviceID {
				continue
			}
			// Extract the AdapterRAM number (first whitespace-delimited token)
			fields := strings.Fields(line)
			if len(fields) > 0 {
				var bytes uint64
				if _, err := fmt.Sscanf(fields[0], "%d", &bytes); err == nil && bytes > 0 {
					return float64(bytes) / (1024 * 1024 * 1024) // Bytes → GiB
				}
			}
		}
	}

	return 0
}

// detectVendorNames queries WMIC for all GPU marketing names.
func detectVendorNames(ctx context.Context) []string {
	out, err := execWithTimeout(ctx, 5*time.Second,
		"wmic", "path", "win32_VideoController", "get", "Name")
	if err != nil {
		return nil
	}

	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || name == "Name" {
			continue
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "microsoft") || strings.Contains(lower, "remote") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// detectRawGPUNames returns a slice of names via ghw.
func detectRawGPUNames() []string {
	gpuInfo, err := ghw.GPU()
	if err != nil || gpuInfo == nil || len(gpuInfo.GraphicsCards) == 0 {
		return nil
	}
	var names []string
	for _, card := range gpuInfo.GraphicsCards {
		if card.DeviceInfo != nil && card.DeviceInfo.Product != nil {
			names = append(names, card.DeviceInfo.Product.Name)
		}
	}
	return names
}

func execLookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

// execWithTimeout runs an external command with a context deadline.
func execWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cmdCtx, name, args...).Output()
}
