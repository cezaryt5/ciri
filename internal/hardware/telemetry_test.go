package hardware

import "testing"

// TestSampleReportsMemory asserts the cross-platform part of the snapshot.
// GPU fields are platform-dependent and zeros are a legal outcome there.
func TestSampleReportsMemory(t *testing.T) {
	m := Sample(nil)
	t.Logf("telemetry snapshot: %+v", m)
	if m.MemTotalGB <= 0 {
		t.Fatalf("expected a nonzero memory total, got %+v", m)
	}
	if m.MemUsedGB < 0 || m.MemUsedGB > m.MemTotalGB {
		t.Fatalf("used memory out of range: %+v", m)
	}
	if m.CPUPercent < 0 || m.CPUPercent > 100 {
		t.Fatalf("cpu percent out of range: %+v", m)
	}
}
