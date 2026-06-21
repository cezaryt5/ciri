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

// detectPCI scans sysfs directly. It is faster and safer than parsing lspci text.
func detectPCI(ctx context.Context) *PCIInfo {
	entries, err := filepath.Glob("/sys/class/drm/card*/device")
	if err != nil || len(entries) == 0 {
		return nil
	}

	var nvidia, amd, intel *PCIInfo

	for _, devDir := range entries {
		i := 0
		vendorID := readHexFile(filepath.Join(devDir, "vendor"))
		deviceID := readHexFile(filepath.Join(devDir, "device"))

		if vendorID == "" || deviceID == "" || vendorID == "0000" {
			continue
		}

		info := &PCIInfo{
			VendorID: vendorID,
			DeviceID: deviceID,
			SysfsDir: devDir,
		}

		switch vendorID {
		case "10de": // NVIDIA
			nvidia = info
		case "1002": // AMD
			amd = info
		case "8086": // Intel
			if intel == nil {
				intel = info
			}

		}
		i++
	}

	// dGPU priority: NVIDIA > AMD > Intel (fallback for iGPU only systems)
	if nvidia != nil {
		return nvidia
	}
	if amd != nil {
		return amd
	}
	return intel
}

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

// detectVRAMLinux guarantees we read the VRAM of the specific PCI device we targeted.
func detectVRAM(ctx context.Context, target *PCIInfo) float64 {
	if target == nil {
		return 0
	}

	// 1. If NVIDIA, nvidia-smi is usually the most accurate source of truth for usable VRAM
	if target.VendorID == "10de" {
		if out, err := execWithTimeout(ctx, 3*time.Second,
			"nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader,nounits"); err == nil {
			var vram float64
			if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &vram); err == nil && vram > 0 {
				return vram / 1024.0 // MiB to GiB
			}
		}
	}

	// 2. Sysfs fallback (Crucial for AMD/Intel).
	// We ONLY read from the specific SysfsDir we mapped to our target PCI card.
	vramPath := filepath.Join(target.SysfsDir, "mem_info_vram_total")
	raw, err := os.ReadFile(vramPath)
	if err == nil {
		var bytes uint64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &bytes); err == nil && bytes > 0 {
			return float64(bytes) / (1024 * 1024 * 1024)
		}
	}

	return 0
}

// ---- Vendor Name Detection ----

func detectVendorName(ctx context.Context, target *PCIInfo) string {
	if target == nil {
		return ""
	}

	if target.VendorID == "10de" {
		if name := nvidiaSMIQuery(ctx); name != "" {
			return name
		}
	}

	if target.VendorID == "1002" {
		if name := rocmSMIQuery(ctx); name != "" {
			return name
		}
	}

	return "" // Let your JSON matching handle the fallback based on VendorID + DeviceID
}

func nvidiaSMIQuery(ctx context.Context) string {
	out, err := execWithTimeout(ctx, 3*time.Second,
		"nvidia-smi", "--query-gpu=name", "--format=csv,noheader")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

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

func detectRawGPUName() string {
	gpuInfo, err := ghw.GPU()
	if err != nil || gpuInfo == nil || len(gpuInfo.GraphicsCards) == 0 {
		return ""
	}

	for _, card := range gpuInfo.GraphicsCards {
		if card == nil || card.DeviceInfo == nil || card.DeviceInfo.Product == nil {
			continue
		}
		name := strings.ToLower(card.DeviceInfo.Product.Name)

		// Filter out obvious iGPUs.
		if strings.Contains(name, "intel hd") ||
			strings.Contains(name, "intel uhd") ||
			strings.Contains(name, "intel iris") {
			continue
		}
		if strings.Contains(name, "radeon") &&
			!strings.Contains(name, "rx") &&
			!strings.Contains(name, "pro") &&
			!strings.Contains(name, "vii") &&
			!strings.Contains(name, "vega") {
			continue
		}

		if strings.Contains(name, "rtx") || strings.Contains(name, "gtx") ||
			strings.Contains(name, "radeon") || strings.Contains(name, "apple") ||
			strings.Contains(name, "quadro") || strings.Contains(name, "tesla") ||
			strings.Contains(name, "arc") {
			return card.DeviceInfo.Product.Name
		}
	}

	if len(gpuInfo.GraphicsCards) > 0 &&
		gpuInfo.GraphicsCards[0].DeviceInfo != nil &&
		gpuInfo.GraphicsCards[0].DeviceInfo.Product != nil {
		return gpuInfo.GraphicsCards[0].DeviceInfo.Product.Name
	}
	return ""
}
