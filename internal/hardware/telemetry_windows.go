//go:build windows

package hardware

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// sampleGPU fills GPU telemetry on Windows. NVIDIA is queried through
// nvidia-smi; AMD/Intel driver counters are not exposed without vendor
// tooling, so those fields stay zero (rendered as dashes).
func sampleGPU(gpu *GPU, m *Metrics) {
	out, err := execWithTimeout(context.Background(), 3*time.Second,
		"nvidia-smi",
		"--query-gpu=utilization.gpu,utilization.memory,memory.used,memory.total,temperature.gpu,power.draw,clocks.sm,clocks.mem,clocks_throttle_reasons.active",
		"--format=csv,noheader,nounits")
	if err != nil {
		return
	}
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	fields := strings.Split(line, ",")
	if len(fields) < 4 {
		return
	}
	if v, ok := parseMetricOK(fields[0]); ok {
		m.GPUUtilPct = v
		m.GPUUtilKnown = true
	}
	if v, ok := parseMetricOK(fields[1]); ok {
		m.GPUMemUtilPct = v
		m.GPUMemUtilKnown = true
	}
	m.VRAMUsedGB = parseMetric(fields[2]) / 1024
	m.VRAMTotalGB = parseMetric(fields[3]) / 1024
	if len(fields) >= 9 {
		m.GPUTempC = parseMetric(fields[4])
		m.GPUPowerW = parseMetric(fields[5])
		m.GPUSMClockMHz = parseMetric(fields[6])
		m.GPUMemClockMHz = parseMetric(fields[7])
		m.GPUThrottle = strings.TrimSpace(fields[8])
	}
}

// parseMetric parses a nvidia-smi numeric field. Placeholders such as
// "[N/A]" or "[Not Supported]" yield zero.
func parseMetric(s string) float64 {
	v, _ := parseMetricOK(s)
	return v
}

// parseMetricOK is parseMetric plus a validity flag, so callers can tell a
// real zero (an idle GPU) from a placeholder the driver does not support.
func parseMetricOK(s string) (float64, bool) {
	var v float64
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &v); err != nil {
		return 0, false
	}
	return v, true
}

// cpuTempC is not exposed by a stable, unprivileged Windows API. Zero means
// the chrome hides the sensor instead of guessing.
func cpuTempC() float64 {
	return 0
}
