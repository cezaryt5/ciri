package hardware

import "context"

// PCIMatcher uses PCI vendor/device IDs for an exact hardware match.
// This is the highest-confidence strategy (0.98).
type PCIMatcher struct{}

// Detect — internal/hardware/matcher_pci.go:9
// Called from: detection.go:147 (via GPUMatcher interface)
// Matches GPU by PCI vendor/device IDs. Scans sysfs (Linux), WMI (Windows),
// or returns nil (macOS). When multiple GPUs share the same PCI ID, uses
// detected VRAM or desktop preference to disambiguate.
func (p PCIMatcher) Detect(ctx context.Context, gpuDB []GPU) (*GPU, float64, error) {
	pciDevices := detectPCI()
	if len(pciDevices) == 0 {
		return nil, 0, nil
	}

	var bestGPU *GPU
	var bestConfidence float64
	var bestVram float64

	for _, pci := range pciDevices {
		matches := findGPUsByPCI(gpuDB, pci.VendorID, pci.DeviceID)
		if len(matches) == 0 {
			continue // unknown iGPU , skip
		}

		vram := detectVRAM(ctx, pci)
		candidate := pickBestPCIMatch(matches, vram)

		conf := 0.98
		if len(matches) > 1 && vram > 0 {
			conf = 0.95
		} else if len(matches) > 1 {
			conf = 0.94
		}

		isBest := false
		if bestGPU == nil {
			isBest = true
		} else if vram > bestVram {
			isBest = true
		} else if vram < bestVram {
			isBest = false
		}

		if isBest == true {
			bestGPU = candidate
			bestConfidence = conf
			bestVram = vram
		}

	}

	if bestGPU == nil {
		return nil, 0, nil
	}

	return bestGPU, bestConfidence, nil
}
