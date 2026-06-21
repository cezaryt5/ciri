package hardware

import "context"

// PCIMatcher uses PCI vendor/device IDs for an exact hardware match.
// This is the highest-confidence strategy (0.98).
type PCIMatcher struct{}

func (p PCIMatcher) Detect(ctx context.Context, gpuDB []GPU) (*GPU, float64, error) {
	pci := detectPCI(ctx)
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
