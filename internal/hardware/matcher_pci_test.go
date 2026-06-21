package hardware

import (
	"context"
	"testing"
)

func TestPCIMatcher_Detect_EmptyDB(t *testing.T) {
	m := &PCIMatcher{}
	gpu, confidence, err := m.Detect(context.Background(), nil)

	// In test env without sysfs/lspci, detectPCI returns nil → no match
	if err != nil {
		t.Logf("detect error (expected in test env): %v", err)
	}
	if gpu != nil {
		t.Errorf("expected nil GPU with empty DB, got %v", gpu.Name)
	}
	if confidence != 0 {
		t.Errorf("expected 0 confidence, got %f", confidence)
	}
}

func TestPCIMatcher_Detect_WithMockDB(t *testing.T) {
	db := []GPU{
		{ID: 1, Name: "NVIDIA GeForce RTX 5090", VendorID: "10de",
			DeviceIDs: []string{"2b85"}, VRAMGB: 32},
	}

	m := &PCIMatcher{}
	gpu, _, err := m.Detect(context.Background(), db)

	if err != nil {
		t.Logf("detect error (expected in test env): %v", err)
	}
	// No real GPU in test env → detectPCI returns nil → no match
	if gpu != nil {
		t.Logf("GPU detected in test env: %v (unexpected but possible)", gpu.Name)
	}
}

// Test that findGPUsByPCI finds the correct GPU when both vendor and device match.
func TestPCIMatcher_FindByPCILogic(t *testing.T) {
	db := []GPU{
		{ID: 1, Name: "RTX 5090", VendorID: "10de", DeviceIDs: []string{"2b85"}, VRAMGB: 32},
		{ID: 2, Name: "RX 9070 XT", VendorID: "1002", DeviceIDs: []string{"7550"}, VRAMGB: 16},
		{ID: 3, Name: "RTX 3080", VendorID: "10de", DeviceIDs: []string{"2206"}, VRAMGB: 10},
	}

	tests := []struct {
		vendor, device string
		expectedID     int
	}{
		{"10de", "2b85", 1},
		{"1002", "7550", 2},
		{"10de", "2206", 3},
		{"10de", "ffff", 0}, // no match
		{"8086", "2b85", 0}, // wrong vendor
	}

	for _, tc := range tests {
		matches := findGPUsByPCI(db, tc.vendor, tc.device)
		if tc.expectedID == 0 {
			if len(matches) != 0 {
				t.Errorf("vendor=%s device=%s: expected 0 matches, got %d",
					tc.vendor, tc.device, len(matches))
			}
		} else {
			if len(matches) != 1 || matches[0].ID != int64(tc.expectedID) {
				t.Errorf("vendor=%s device=%s: expected GPU ID %d, got %v",
					tc.vendor, tc.device, tc.expectedID, matches)
			}
		}
	}
}

func TestVendorAPIMatcher_ResolveByName(t *testing.T) {
	db := []GPU{
		{ID: 1, Name: "NVIDIA GeForce RTX 5090",
			CanonicalName: "rtx 5090",
			Aliases:        []string{"rtx 5090"}},
		{ID: 2, Name: "AMD Radeon RX 7900 XTX",
			CanonicalName: "rx 7900 xtx",
			Aliases:        []string{"rx 7900 xtx"}},
	}

	tests := []struct {
		query       string
		expectedID  int
		expectMatch bool
	}{
		{"NVIDIA GeForce RTX 5090", 1, true},
		{"nvidia geforce rtx 5090", 1, true},
		{"rtx 5090", 1, true}, // alias match
		{"rx 7900 xtx", 2, true}, // alias match
		{"RTX 3050", 0, false},   // not in DB
		{"", 0, false},
	}

	for _, tc := range tests {
		gpu, confidence, _ := resolveByName(db, tc.query, 0.95)
		if tc.expectMatch {
			if gpu == nil {
				t.Errorf("resolveByName(%q): expected match, got nil", tc.query)
				continue
			}
			if gpu.ID != int64(tc.expectedID) {
				t.Errorf("resolveByName(%q): got ID %d, want %d", tc.query, gpu.ID, tc.expectedID)
			}
			if confidence <= 0 || confidence > 1.0 {
				t.Errorf("resolveByName(%q): confidence %f out of range", tc.query, confidence)
			}
		} else {
			if gpu != nil {
				t.Errorf("resolveByName(%q): expected nil, got %v", tc.query, gpu.Name)
			}
		}
	}
}

func TestGHWFuzzyMatcher_TokenOverlapScore(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		// Both have >= 2 tokens after normalization.
		{"rtx 4090", "rtx 4090", 2},         // "rtx" + "4090"
		{"rtx 4090", "rtx 5090", 1},         // only "rtx"
		{"rx 7900 xtx", "rx 7800 xt", 1},    // only "rx"
		{"rx 7900 xtx", "rx 7900 xtx", 3},   // "rx" + "7900" + "xtx"
		// "nvidia geforce rtx 4090" normalizes to "4090" (1 token) → falls < 2 → score 0.
		{"nvidia geforce rtx 4090", "rtx 4090", 0},
		{"apple m4 max", "intel core i9", 0}, // no overlap
		{"", "", 0},
		{"a", "a", 0}, // tokens < 2 chars
	}

	for _, tc := range tests {
		got := tokenOverlapScore(tc.a, tc.b)
		if got != tc.expected {
			t.Errorf("tokenOverlapScore(%q, %q) = %d, want %d",
				tc.a, tc.b, got, tc.expected)
		}
	}
}

func TestGHWFuzzyMatcher_Detect_EmptyDB(t *testing.T) {
	m := &GHWFuzzyMatcher{}
	gpu, confidence, err := m.Detect(context.Background(), nil)

	if err != nil {
		t.Logf("detect error (expected in test env): %v", err)
	}
	// Empty DB: PCI + Vendor matchers fail.  GHW matcher may still detect raw
	// hardware on a real machine.  Accept nil, or a bare GPU with confidence < 0.5.
	if gpu != nil && gpu.Name == "" {
		t.Error("GPU should have a name if detected")
	}
	if gpu != nil && confidence == 0 {
		t.Errorf("confidence should be > 0 when GPU is detected, got %f", confidence)
	}
}

func TestGHWFuzzyMatcher_Detect_WithDB(t *testing.T) {
	db := []GPU{
		{ID: 1, Name: "NVIDIA GeForce RTX 4090", CanonicalName: "rtx 4090",
			Aliases: []string{"rtx 4090"}},
		{ID: 2, Name: "AMD Radeon RX 7900 XTX", CanonicalName: "rx 7900 xtx",
			Aliases: []string{"rx 7900 xtx"}},
	}

	m := &GHWFuzzyMatcher{}
	gpu, conf, _ := m.Detect(context.Background(), db)

	// On a real machine, GHW may detect the actual GPU.
	// If detected and the DB has it, confidence should be reasonable.
	if gpu != nil {
		if conf <= 0 {
			t.Errorf("confidence should be > 0 for detected GPU, got %f", conf)
		}
		if conf > 1.0 {
			t.Errorf("confidence should be <= 1.0, got %f", conf)
		}
	}
}
