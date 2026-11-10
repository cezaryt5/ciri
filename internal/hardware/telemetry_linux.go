//go:build linux

package hardware

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sampleGPU fills GPU telemetry on Linux. NVIDIA is queried through
// nvidia-smi (the same tool the detection cascade already shells out to);
// everything else falls back to the amdgpu sysfs counters.
func sampleGPU(gpu *GPU, m *Metrics) {
	vendor := ""
	if gpu != nil {
		vendor = strings.ToLower(gpu.VendorID)
	}

	if vendor != "1002" { // skip nvidia-smi when an AMD card was detected
		if out, err := execWithTimeout(context.Background(), 2500*time.Millisecond,
			"nvidia-smi",
			"--query-gpu=utilization.gpu,utilization.memory,memory.used,memory.total,temperature.gpu,power.draw,clocks.sm,clocks.mem,clocks_throttle_reasons.active",
			"--format=csv,noheader,nounits"); err == nil {
			line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
			fields := strings.Split(line, ",")
			if len(fields) >= 4 {
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
				return
			}
		}
	}
	sampleAMDGPU(m)
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

// sampleAMDGPU reads the amdgpu driver's sysfs counters: busy percentage,
// VRAM usage and the hwmon temperature/average power nodes.
func sampleAMDGPU(m *Metrics) {
	cards, _ := filepath.Glob("/sys/class/drm/card[0-9]*")
	for _, card := range cards {
		dev := filepath.Join(card, "device")
		raw, err := os.ReadFile(filepath.Join(dev, "gpu_busy_percent"))
		if err != nil {
			continue
		}
		m.GPUUtilPct = parseMetric(string(raw))
		m.GPUUtilKnown = true
		if raw, err := os.ReadFile(filepath.Join(dev, "mem_info_vram_used")); err == nil {
			m.VRAMUsedGB = parseMetric(string(raw)) / (1 << 30)
		}
		if raw, err := os.ReadFile(filepath.Join(dev, "mem_info_vram_total")); err == nil {
			m.VRAMTotalGB = parseMetric(string(raw)) / (1 << 30)
		}
		if raw, err := os.ReadFile(filepath.Join(dev, "mem_busy_percent")); err == nil {
			if v, ok := parseMetricOK(string(raw)); ok {
				m.GPUMemUtilPct = v
				m.GPUMemUtilKnown = true
			}
		}
		if hwmons, _ := filepath.Glob(filepath.Join(dev, "hwmon", "hwmon*")); len(hwmons) > 0 {
			if raw, err := os.ReadFile(filepath.Join(hwmons[0], "temp1_input")); err == nil {
				m.GPUTempC = parseMetric(string(raw)) / 1000
			}
			if raw, err := os.ReadFile(filepath.Join(hwmons[0], "power1_average")); err == nil {
				m.GPUPowerW = parseMetric(string(raw)) / 1e6
			}
		}
		return
	}
}

// cpuTempC returns the hottest CPU package temperature reported by the
// coretemp/k10temp style hwmon drivers. Zero when no sensor exists.
func cpuTempC() float64 {
	sensorNames := map[string]bool{
		"coretemp": true, "k10temp": true, "zenpower": true, "cpu_thermal": true,
	}
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	best := 0.0
	for _, hw := range hwmons {
		rawName, err := os.ReadFile(filepath.Join(hw, "name"))
		if err != nil || !sensorNames[strings.TrimSpace(string(rawName))] {
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(hw, "temp*_input"))
		for _, in := range inputs {
			raw, err := os.ReadFile(in)
			if err != nil {
				continue
			}
			if c := parseMetric(string(raw)) / 1000; c > best && c < 150 {
				best = c
			}
		}
	}
	return best
}
