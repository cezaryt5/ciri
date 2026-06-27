//go:build linux

package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"path/filepath"
	"strings"
	"time"

	"github.com/jaypipes/ghw"
)

// ---- PCI Detection ----

// detectPCI — internal/hardware/detection_linux.go:24
// Called from: matcher_pci.go:15
// Scans /sys/bus/pci/devices/* for vendor/device IDs. Returns all GPUs
// found (class 03xxxx — VGA and 3D controllers).
func detectPCI() []*PCIInfo {
	// i will uery the PCI bus directly. This sees the hardware even if drivers aren't loaded.
	entries, err := filepath.Glob("/sys/bus/pci/devices/*")
	if err != nil || len(entries) == 0 {
		return nil
	}

	var results []*PCIInfo

	for _, devDir := range entries {
		class := readHexFile(filepath.Join(devDir, "class"))

		// The class file in sysfs PCI contains a 6-digit hex (e.g., 030000).
		// 030000 = VGA compatible controller (Your RTX 3060 desktop setup)
		// 030200 = 3D controller (Your GTX 1070 Optimus laptop setup behind the Intel iGPU)
		if !strings.HasPrefix(class, "03") {
			continue
		}

		vendorID := readHexFile(filepath.Join(devDir, "vendor"))
		deviceID := readHexFile(filepath.Join(devDir, "device"))

		if vendorID == "" || deviceID == "" || vendorID == "0000" {
			continue
		}

		info := &PCIInfo{
			VendorID: vendorID,
			DeviceID: deviceID,
			SysfsDir: devDir, // This path is now far more useful for reading PCIe link speeds/widths later
		}

		results = append(results, info)
	}
	return results
}

// readHexFile — internal/hardware/detection_linux.go:68
// Called from: detection_linux.go:31-32 (in detectPCI); detection_linux_test.go:49,54,66,78,85
// Reads a hex value from a sysfs file. Strips "0x" prefix and whitespace.
// Returns the lowercase hex string, or "" on error/missing file.
func readHexFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	val := strings.TrimSpace(string(raw))
	val = strings.TrimPrefix(strings.ToLower(val), "0x")
	return val
}

// ---- VRAM Detection ----

// detectVRAM — internal/hardware/detection_linux.go:82
// Called from: matcher_pci.go:30; detection_linux_test.go:91
// Detects VRAM of the targeted PCI device. For NVIDIA GPUs, uses nvidia-smi
// for accuracy. Falls back to sysfs (mem_info_vram_total) for AMD/Intel.
// Returns GiB, or 0 on failure.
func detectVRAM(ctx context.Context, target *PCIInfo) float64 {
	if target == nil {
		return 0
	}

	// Extract the PCI Bus ID from the sysfs path (e.g., "0000:01:00.0")
	pciID := filepath.Base(target.SysfsDir)
	pciID = strings.TrimPrefix(pciID, "0000:")
	// 1. NVIDIA strict targeting
	if target.VendorID == "10de" {
		// We pass '-i pciID' so we don't cross-contaminate VRAM readings on multi-GPU setups
		if out, err := execWithTimeout(ctx, 3*time.Second,
			"nvidia-smi", "-i", pciID, "--query-gpu=memory.total", "--format=csv,noheader,nounits"); err == nil {

			var vram float64
			if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &vram); err == nil && vram > 0 {
				return vram / 1024.0 // MiB to GiB
			}
		}
	}

	// 2. Sysfs fallback (Works for AMD, fails silently for Intel)
	vramPath := filepath.Join(target.SysfsDir, "mem_info_vram_total")
	if raw, err := os.ReadFile(vramPath); err == nil {
		var bytes uint64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &bytes); err == nil && bytes > 0 {
			return float64(bytes) / (1024 * 1024 * 1024) // Bytes to GiB
		}
	}

	return 0
}

// ---- Vendor Name Detection ----

// detectVendorNames — internal/hardware/detection_linux.go:122
// Called from: matcher_vendor.go:20
// Queries the vendor-specific CLI tool (nvidia-smi for NVIDIA, rocm-smi for
// AMD) to obtain the GPU marketing name. Returns "" if the tool is unavailable
// or the vendor is not recognised.
func detectVendorNames(ctx context.Context) []string {
	var names []string

	// NVIDIA
	if nvidiaName := nvidiaSMIQuery(ctx); nvidiaName != "" {
		names = append(names, nvidiaName)
	}

	// AMD
	if rocmName := rocmSMIQuery(ctx); rocmName != "" {
		names = append(names, rocmName)
	}

	return names
}

// nvidiaSMIQuery — internal/hardware/detection_linux.go:142
// Called from: detection_linux.go:129 (in detectVendorNames)
// Executes nvidia-smi --query-gpu=name and returns the trimmed output, or ""
// on error.
func nvidiaSMIQuery(ctx context.Context) string {
	out, err := execWithTimeout(ctx, 3*time.Second,
		"nvidia-smi", "--query-gpu=name", "--format=csv,noheader,nounits")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// rocmSMIQuery — internal/hardware/detection_linux.go:155
// Called from: detection_linux.go:134 (in detectVendorNames)
// Executes rocm-smi --showproductname --json and parses the JSON output for
// "Card series" or "Card model" fields. Returns the GPU name, or "" on error.
func rocmSMIQuery(ctx context.Context) string {
	// Never parse AMD text output. It changes. Use JSON.
	out, err := execWithTimeout(ctx, 3*time.Second, "rocm-smi", "--showproductname", "--json")
	if err != nil {
		return ""
	}

	var data map[string]map[string]string
	if err := json.Unmarshal(out, &data); err != nil {
		return ""
	}

	// rocm-smi json structure is usually: {"card0": {"Card series": "Radeon RX 7900 XTX"}}
	for _, cardData := range data {
		if series, ok := cardData["Card series"]; ok {
			return strings.TrimSpace(series)
		}
		if model, ok := cardData["Card model"]; ok {
			return strings.TrimSpace(model)
		}
	}
	return ""
}

// detectRawGPUNames — internal/hardware/detection_linux.go:184
// Called from: matcher_ghw.go:19
// Uses ghw to enumerate GPUs, filters out integrated graphics (Intel HD/UHD/Iris,
// non-RX AMD Radeon), and returns all discrete GPU product names.
func detectRawGPUNames() []string {
	gpuInfo, err := ghw.GPU()
	if err != nil || gpuInfo == nil || len(gpuInfo.GraphicsCards) == 0 {
		return nil
	}

	var validGPUs []string

	for _, card := range gpuInfo.GraphicsCards {
		if card == nil || card.DeviceInfo == nil || card.DeviceInfo.Product == nil {
			continue
		}
		name := strings.ToLower(card.DeviceInfo.Product.Name)

		// 1. Filter out all Intel integrated graphics (including the new Arc iGPUs)
		// We catch Arc iGPUs by looking for standard laptop naming schemes (e.g., "130v", "140v", "8-core")
		// while leaving desktop Arc (A770, B580) alone.
		if strings.Contains(name, "intel hd") ||
			strings.Contains(name, "intel uhd") ||
			strings.Contains(name, "intel iris") ||
			strings.Contains(name, "graphics 1") || // Catches Arc 130V/140V
			strings.Contains(name, "core igpu") || // Catches "Arc 8-Core iGPU"
			name == "intel graphics" || name == "intel arc graphics" {
			continue
		}

		// 2. Filter out AMD iGPUs (780M, 890M, generic Radeon Graphics)
		if strings.Contains(name, "radeon") &&
			!strings.Contains(name, "rx") &&
			!strings.Contains(name, "pro") &&
			!strings.Contains(name, "vii") &&
			!strings.Contains(name, "vega") &&
			!strings.Contains(name, "xt") { // Added XT just to be safe
			continue
		}
		validGPUs = append(validGPUs, card.DeviceInfo.Product.Name)

	}

	return validGPUs
}
