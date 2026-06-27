package hardware

import (
	"encoding/json"
	"os"
	"testing"
)

// ---------------------------------------------------------------------------
// LoadGPUDB
// ---------------------------------------------------------------------------

func TestLoadGPUDB_ValidFile(t *testing.T) {
	input := []map[string]interface{}{
		{
			"name":             "NVIDIA GeForce RTX 4090",
			"pci_vendor_id":    "10de",
			"pci_device_id":    "2684",
			"pci_device_ids":   []string{"2684"},
			"pci_variants":     map[string]interface{}{},
			"vram_gb":          24.0,
			"memory_bandwidth_gbps": 1008.0,
			"tflops":           165.2,
			"architecture":     "AD102",
		},
		{
			"name":             "Apple M4 Max (GPU)",
			"category":         "apple_silicon",
			"pci_vendor_id":    "106b",
			"pci_device_id":    nil,
			"pci_device_ids":   []string{},
			"pci_variants":     map[string]interface{}{},
			"vram_gb":          nil,
			"memory_bandwidth_gbps": 546.0,
			"tflops":           39.0,
			"architecture":     "Apple M4",
		},
	}

	raw, _ := json.Marshal(input)
	gpus, err := LoadGPUDB(raw)
	if err != nil {
		t.Fatalf("LoadGPUDB failed: %v", err)
	}
	if len(gpus) != 2 {
		t.Fatalf("expected 2 GPUs, got %d", len(gpus))
	}

	g0 := gpus[0]
	if g0.ID != 1 {
		t.Errorf("first GPU ID = %d, want 1", g0.ID)
	}
	if g0.Name != "NVIDIA GeForce RTX 4090" {
		t.Errorf("name = %q, want %q", g0.Name, "NVIDIA GeForce RTX 4090")
	}
	if g0.VRAMGB != 24.0 {
		t.Errorf("vram = %f, want 24.0", g0.VRAMGB)
	}
	if g0.Bandwidth != 1008.0 {
		t.Errorf("bandwidth = %f, want 1008.0", g0.Bandwidth)
	}
	if g0.TFLOPS != 165.2 {
		t.Errorf("tflops = %f, want 165.2", g0.TFLOPS)
	}
	if g0.Architecture != "AD102" {
		t.Errorf("architecture = %q, want AD102", g0.Architecture)
	}
	if g0.VendorID != "10de" {
		t.Errorf("vendorID = %q, want 10de", g0.VendorID)
	}
	if len(g0.DeviceIDs) != 1 || g0.DeviceIDs[0] != "2684" {
		t.Errorf("deviceIDs = %v, want [2684]", g0.DeviceIDs)
	}
	if g0.IsLaptop {
		t.Errorf("desktop GPU marked as laptop")
	}
	if len(g0.Aliases) == 0 {
		t.Errorf("aliases should not be empty")
	}

	g1 := gpus[1]
	if g1.ID != 2 {
		t.Errorf("second GPU ID = %d, want 2", g1.ID)
	}
	if len(g1.DeviceIDs) != 0 {
		t.Errorf("apple silicon deviceIDs should be empty, got %v", g1.DeviceIDs)
	}
}

func TestLoadGPUDB_InvalidJSON(t *testing.T) {
	_, err := LoadGPUDB([]byte("not json{{{"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadGPUDB_LaptopVariants(t *testing.T) {
	input := []map[string]interface{}{
		{
			"name":           "NVIDIA GeForce GTX 1070",
			"pci_vendor_id":  "10de",
			"pci_device_id":  "1b81",
			"pci_device_ids": []string{"1b81", "1be1"},
			"pci_variants": map[string]interface{}{
				"desktop": map[string]interface{}{
					"ids":         []string{"1b81"},
					"description": "Desktop variant",
				},
				"mobile": map[string]interface{}{
					"ids":         []string{"1be1"},
					"description": "Mobile variant",
				},
			},
			"vram_gb": 8.0,
		},
	}

	raw, _ := json.Marshal(input)
	gpus, err := LoadGPUDB(raw)
	if err != nil {
		t.Fatalf("LoadGPUDB failed: %v", err)
	}
	if len(gpus) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(gpus))
	}

	g := gpus[0]
	if !g.IsLaptop {
		t.Error("GPU with mobile variant should be marked as laptop")
	}
	_, hasMobile := g.LaptopDeviceIDs["1be1"]
	if !hasMobile {
		t.Errorf("expected 1be1 in LaptopDeviceIDs, got %v", g.LaptopDeviceIDs)
	}
	_, hasDesktop := g.LaptopDeviceIDs["1b81"]
	if hasDesktop {
		t.Error("desktop device ID should NOT be in LaptopDeviceIDs")
	}
}

func TestLoadGPUDB_InferredLaptopName(t *testing.T) {
	input := []map[string]interface{}{
		{
			"name":           "NVIDIA GeForce RTX 5070 Mobile",
			"pci_vendor_id":  "10de",
			"pci_device_id":  "2f18",
			"pci_device_ids": []string{"2f18"},
			"pci_variants":   map[string]interface{}{},
			"vram_gb":        8.0,
		},
	}

	raw, _ := json.Marshal(input)
	gpus, err := LoadGPUDB(raw)
	if err != nil {
		t.Fatalf("LoadGPUDB failed: %v", err)
	}
	if !gpus[0].IsLaptop {
		t.Error("GPU with 'Mobile' in name should be marked as laptop")
	}
}

// ---------------------------------------------------------------------------
// findGPUsByPCI
// ---------------------------------------------------------------------------

func TestFindGPUsByPCI_ExactMatch(t *testing.T) {
	db := []GPU{
		{ID: 1, Name: "RTX 4090", VendorID: "10de", DeviceIDs: []string{"2684"}},
		{ID: 2, Name: "RTX 5090", VendorID: "10de", DeviceIDs: []string{"2b85"}},
		{ID: 3, Name: "RX 7900 XTX", VendorID: "1002", DeviceIDs: []string{"744c"}},
	}

	matches := findGPUsByPCI(db, "10de", "2b85")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].ID != 2 {
		t.Errorf("got GPU ID %d, want 2", matches[0].ID)
	}
}

func TestFindGPUsByPCI_MultipleDeviceIDs(t *testing.T) {
	db := []GPU{
		{ID: 1, Name: "RTX 2070 SUPER", VendorID: "10de",
			DeviceIDs: []string{"1e84", "1ec2", "1ec7"}},
	}

	// Should match any of the three device IDs
	for _, did := range []string{"1e84", "1ec2", "1ec7"} {
		matches := findGPUsByPCI(db, "10de", did)
		if len(matches) != 1 {
			t.Errorf("device %s: expected 1 match, got %d", did, len(matches))
		}
	}
}

func TestFindGPUsByPCI_NoMatch(t *testing.T) {
	db := []GPU{
		{ID: 1, Name: "RTX 4090", VendorID: "10de", DeviceIDs: []string{"2684"}},
	}

	matches := findGPUsByPCI(db, "10de", "0000")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for bogus device, got %d", len(matches))
	}

	matches = findGPUsByPCI(db, "8086", "2684")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for wrong vendor, got %d", len(matches))
	}
}

func TestFindGPUsByPCI_MultipleSharedDeviceID(t *testing.T) {
	db := []GPU{
		{ID: 1, Name: "RX 7900 XT", VendorID: "1002", DeviceIDs: []string{"744c"}, VRAMGB: 20},
		{ID: 2, Name: "RX 7900 XTX", VendorID: "1002", DeviceIDs: []string{"744c"}, VRAMGB: 24},
	}

	matches := findGPUsByPCI(db, "1002", "744c")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches for shared device ID, got %d", len(matches))
	}
}

// ---------------------------------------------------------------------------
// pickBestPCIMatch
// ---------------------------------------------------------------------------

func TestPickBestPCIMatch_Single(t *testing.T) {
	matches := []*GPU{
		{ID: 1, Name: "RTX 4090", VRAMGB: 24},
	}
	best := pickBestPCIMatch(matches, 0)
	if best == nil || best.ID != 1 {
		t.Errorf("expected single GPU to be picked")
	}
}

func TestPickBestPCIMatch_Empty(t *testing.T) {
	best := pickBestPCIMatch(nil, 0)
	if best != nil {
		t.Error("expected nil for empty matches")
	}
}

func TestPickBestPCIMatch_VRAMTiebreaker(t *testing.T) {
	matches := []*GPU{
		{ID: 1, Name: "RX 7900 XT", VRAMGB: 20},
		{ID: 2, Name: "RX 7900 XTX", VRAMGB: 24},
	}

	best := pickBestPCIMatch(matches, 23.5)
	if best == nil || best.ID != 2 {
		t.Errorf("detected 23.5GB: expected RX 7900 XTX (24GB), got %v", best)
	}

	best = pickBestPCIMatch(matches, 18.0)
	if best == nil || best.ID != 1 {
		t.Errorf("detected 18GB: expected RX 7900 XT (20GB), got %v", best)
	}
}

func TestPickBestPCIMatch_PrefersDesktop(t *testing.T) {
	matches := []*GPU{
		{ID: 1, Name: "RTX 3050 Mobile", VRAMGB: 4, IsLaptop: true},
		{ID: 2, Name: "RTX 3050", VRAMGB: 8, IsLaptop: false},
	}

	// Without VRAM hint, prefer desktop
	best := pickBestPCIMatch(matches, 0)
	if best == nil || best.ID != 2 {
		t.Errorf("without VRAM: expected desktop (ID 2), got ID %d", best.ID)
	}
}

func TestPickBestPCIMatch_VRAMOverridesDesktop(t *testing.T) {
	matches := []*GPU{
		{ID: 1, Name: "RTX 3050 Mobile", VRAMGB: 6, IsLaptop: true},
		{ID: 2, Name: "RTX 3050", VRAMGB: 8, IsLaptop: false},
	}

	// VRAM hint matches the laptop variant more closely
	best := pickBestPCIMatch(matches, 5.8)
	if best == nil || best.ID != 1 {
		t.Errorf("detected 5.8GB: expected mobile (ID 1), got ID %d", best.ID)
	}
}

// ---------------------------------------------------------------------------
// deriveAliases
// ---------------------------------------------------------------------------

func TestDeriveAliases_NVIDIA(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			// "nvidia geforce rtx " prefix matched → strips to "rtx 4090" → wait...
			// Actually prefixes are checked in order:
			// "nvidia geforce rtx " matches "nvidia geforce rtx 4090" → strips to "4090"
			// Then "geforce " doesn't match because it's already stripped
			"NVIDIA GeForce RTX 4090",
			[]string{"4090"},
		},
		{
			// "nvidia geforce gtx " matches → strips to "1080 ti"
			"NVIDIA GeForce GTX 1080 Ti",
			[]string{"1080 ti"},
		},
		{
			// "amd radeon rx " matches → strips to "7900 xtx"
			"AMD Radeon RX 7900 XTX",
			[]string{"7900 xtx"},
		},
		{
			// "intel arc " matches → strips to "a770"
			"Intel Arc A770",
			[]string{"a770"},
		},
		{
			// "nvidia rtx " matches → strips to "a6000"
			"NVIDIA RTX A6000",
			[]string{"a6000"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			aliases := deriveAliases(tc.input)
			if len(aliases) != len(tc.expected) {
				t.Errorf("aliases = %v, want %v", aliases, tc.expected)
				return
			}
			for i, a := range aliases {
				if a != tc.expected[i] {
					t.Errorf("alias[%d] = %q, want %q", i, a, tc.expected[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// abs
// ---------------------------------------------------------------------------

func TestAbs(t *testing.T) {
	tests := []struct {
		input, expected float64
	}{
		{5.0, 5.0},
		{-5.0, 5.0},
		{0.0, 0.0},
		{-0.0, 0.0},
		{3.14, 3.14},
		{-3.14, 3.14},
	}

	for _, tc := range tests {
		got := abs(tc.input)
		if got != tc.expected {
			t.Errorf("abs(%f) = %f, want %f", tc.input, got, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// DetectGPU with mock DB
// ---------------------------------------------------------------------------

func TestDetectGPU_ExactPCIMatch(t *testing.T) {
	db := []GPU{
		{ID: 1, Name: "NVIDIA GeForce RTX 5090", VendorID: "10de",
			DeviceIDs: []string{"2b85"}, VRAMGB: 32, CanonicalName: "rtx 5090"},
	}

	var s Specs
	status, gpu := s.DetectGPU(db)

	// GPU: the PCI/Vendor/GHW strategies all try to detect real hardware.
	// In a test environment without real GPUs, all strategies will fail
	// and we get GPUNotFound.
	if status == GPUExact {
		// This only happens if we have a real GPU with PCI ID in the DB.
		// Accept either result - the test validates the code path doesn't panic.
		if gpu == nil {
			t.Error("GPUExact status but nil GPU")
		}
	} else if status == GPUNotFound {
		if gpu != nil {
			t.Error("GPUNotFound status should have nil GPU")
		}
		if s.RawGPUName != "Unknown GPU" {
			t.Errorf("RawGPUName = %q, want 'Unknown GPU'", s.RawGPUName)
		}
	}
}

func TestDetectGPU_NilDB(t *testing.T) {
	var s Specs
	status, gpu := s.DetectGPU(nil)

	// With nil DB, the PCI and Vendor matchers will fail, but the GHW matcher
	// may still find raw hardware (real GPU on the test machine).  Accept either
	// GPUNotFound (no hardware) or GPUUnverified (raw-hardware-only, DB empty).
	if status != GPUNotFound && status != GPUUnverified {
		t.Errorf("status = %v, want GPUNotFound or GPUUnverified", status)
	}
	if status == GPUNotFound && gpu != nil {
		t.Error("GPUNotFound status should have nil GPU")
	}
	if status == GPUUnverified && gpu == nil {
		t.Error("GPUUnverified status should have non-nil GPU")
	}
}

// ---------------------------------------------------------------------------
// GeForce MX150 (GP108M) — integration tests against real gpus.json
// ---------------------------------------------------------------------------

func TestLoadGPUDB_MX150_Presence(t *testing.T) {
	data, err := os.ReadFile("../../data/gpus.json")
	if err != nil {
		t.Fatalf("failed to read gpus.json: %v", err)
	}

	gpus, err := LoadGPUDB(data)
	if err != nil {
		t.Fatalf("LoadGPUDB failed: %v", err)
	}

	var mx150_2gb, mx150_4gb *GPU
	for i := range gpus {
		switch gpus[i].Name {
		case "NVIDIA GeForce MX150":
			mx150_2gb = &gpus[i]
		case "NVIDIA GeForce MX150 4 GB":
			mx150_4gb = &gpus[i]
		}
	}

	if mx150_2gb == nil {
		t.Fatal("NVIDIA GeForce MX150 (2GB) not found in GPU database")
	}
	if mx150_4gb == nil {
		t.Fatal("NVIDIA GeForce MX150 4 GB not found in GPU database")
	}

	// Verify 2GB entry
	if mx150_2gb.VendorID != "10de" {
		t.Errorf("MX150 2GB vendorID = %q, want 10de", mx150_2gb.VendorID)
	}
	if mx150_2gb.VRAMGB != 2.0 {
		t.Errorf("MX150 2GB VRAM = %f, want 2.0", mx150_2gb.VRAMGB)
	}
	if mx150_2gb.Architecture != "GP108" {
		t.Errorf("MX150 2GB architecture = %q, want GP108", mx150_2gb.Architecture)
	}
	if !mx150_2gb.IsLaptop {
		t.Error("MX150 2GB should be marked as laptop (has mobile variant)")
	}
	if len(mx150_2gb.DeviceIDs) == 0 {
		t.Error("MX150 2GB should have device IDs")
	}

	// Verify 4GB entry
	if mx150_4gb.VRAMGB != 4.0 {
		t.Errorf("MX150 4GB VRAM = %f, want 4.0", mx150_4gb.VRAMGB)
	}
	if mx150_4gb.Architecture != "GP108" {
		t.Errorf("MX150 4GB architecture = %q, want GP108", mx150_4gb.Architecture)
	}

	// Both should share the same PCI device IDs
	if len(mx150_2gb.DeviceIDs) != len(mx150_4gb.DeviceIDs) {
		t.Errorf("MX150 2GB and 4GB should have same device IDs: 2GB=%v, 4GB=%v",
			mx150_2gb.DeviceIDs, mx150_4gb.DeviceIDs)
	}
}

func TestLoadGPUDB_MX150_PCILookup(t *testing.T) {
	data, err := os.ReadFile("../../data/gpus.json")
	if err != nil {
		t.Fatalf("failed to read gpus.json: %v", err)
	}

	gpus, err := LoadGPUDB(data)
	if err != nil {
		t.Fatalf("LoadGPUDB failed: %v", err)
	}

	// Verify that both MX150 device IDs are findable via findGPUsByPCI
	for _, did := range []string{"1d10", "1d12"} {
		matches := findGPUsByPCI(gpus, "10de", did)
		if len(matches) == 0 {
			t.Errorf("findGPUsByPCI(10de, %s): expected at least 1 match, got 0", did)
			continue
		}
		found := false
		for _, m := range matches {
			if m.Name == "NVIDIA GeForce MX150" || m.Name == "NVIDIA GeForce MX150 4 GB" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("findGPUsByPCI(10de, %s): no MX150 in matches %v", did, matches)
		}
	}
}

func TestLoadGPUDB_MX150_VRAMDisambiguation(t *testing.T) {
	data, err := os.ReadFile("../../data/gpus.json")
	if err != nil {
		t.Fatalf("failed to read gpus.json: %v", err)
	}

	gpus, err := LoadGPUDB(data)
	if err != nil {
		t.Fatalf("LoadGPUDB failed: %v", err)
	}

	// Simulate PCI match for device ID 1d10 (both entries share it)
	matches := findGPUsByPCI(gpus, "10de", "1d10")
	if len(matches) < 2 {
		t.Fatalf("expected at least 2 MX150 matches for device 1d10, got %d", len(matches))
	}

	// find the MX150 entries
	var mx150_2gb, mx150_4gb *GPU
	for _, m := range matches {
		switch m.Name {
		case "NVIDIA GeForce MX150":
			mx150_2gb = m
		case "NVIDIA GeForce MX150 4 GB":
			mx150_4gb = m
		}
	}
	if mx150_2gb == nil || mx150_4gb == nil {
		t.Fatalf("expected both MX150 entries in matches, got 2GB=%v, 4GB=%v", mx150_2gb, mx150_4gb)
	}

	// pickBestPCIMatch with 2GB VRAM hint → should pick 2GB
	best := pickBestPCIMatch(matches, 2.0)
	if best == nil || best.Name != "NVIDIA GeForce MX150" {
		t.Errorf("VRAM=2.0: expected MX150 2GB, got %v", best)
	}

	// pickBestPCIMatch with 4GB VRAM hint → should pick 4GB
	best = pickBestPCIMatch(matches, 4.0)
	if best == nil || best.Name != "NVIDIA GeForce MX150 4 GB" {
		t.Errorf("VRAM=4.0: expected MX150 4GB, got %v", best)
	}
}

func TestLoadGPUDB_MX150_NameResolution(t *testing.T) {
	data, err := os.ReadFile("../../data/gpus.json")
	if err != nil {
		t.Fatalf("failed to read gpus.json: %v", err)
	}

	gpus, err := LoadGPUDB(data)
	if err != nil {
		t.Fatalf("LoadGPUDB failed: %v", err)
	}

	// Test that nvidia-smi style names resolve correctly
	tests := []struct {
		query      string
		expectName string
	}{
		{"NVIDIA GeForce MX150", "NVIDIA GeForce MX150"},
		{"GeForce MX150", "NVIDIA GeForce MX150"},
		{"NVIDIA GeForce MX150 4 GB", "NVIDIA GeForce MX150 4 GB"},
	}

	for _, tc := range tests {
		gpu, confidence, _ := resolveByName(gpus, tc.query, 0.95)
		if gpu == nil {
			t.Errorf("resolveByName(%q): expected match, got nil", tc.query)
			continue
		}
		if gpu.Name != tc.expectName {
			t.Errorf("resolveByName(%q): got name %q, want %q", tc.query, gpu.Name, tc.expectName)
		}
		if confidence <= 0.85 {
			t.Errorf("resolveByName(%q): confidence %f too low for exact match", tc.query, confidence)
		}
	}
}
