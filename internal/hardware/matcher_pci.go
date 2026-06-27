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
	pci := detectPCI()
	if pci == nil {
		return nil, 0, nil
	}

	matches := findGPUsByPCI(gpuDB, pci.VendorID, pci.DeviceID)
	if len(matches) == 0 {
		return nil, 0, nil
	}

	vram := detectVRAM(ctx, pci)
	best := pickBestPCIMatch(matches, vram)

	confidence := 0.98
	if len(matches) > 1 && vram > 0 {
		confidence = 0.96
	} else if len(matches) > 1 {
		confidence = 0.94 // ambiguous, falls to VendorAPIMatcher
	}

	return best, confidence, nil
}
