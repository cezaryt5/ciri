package hardware

import (
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

// cpuPrimed tracks whether the CPU sampler has a previous measurement to
// diff against. The very first Percent(0) call has nothing to compare with
// and reports 0, so it briefly blocks for a real interval instead.
var cpuPrimed bool

// Metrics is one point-in-time snapshot of machine load for the chrome strip.
// A zero value means "not available on this platform/GPU" — the TUI renders a
// dash for those, never a fabricated number.
type Metrics struct {
	CPUPercent      float64 // 0-100 across all cores
	CPUPercentKnown bool    // distinguishes an idle 0% from an unavailable sample
	CPUTempC        float64 // hottest CPU package sensor, degrees Celsius
	MemUsedGB       float64
	MemTotalGB      float64
	GPUUtilPct      float64 // 0-100
	GPUUtilKnown    bool    // distinguishes an idle 0% from an unavailable sample
	GPUMemUtilPct   float64 // memory-controller utilization
	GPUMemUtilKnown bool
	VRAMUsedGB      float64
	VRAMTotalGB     float64
	GPUTempC        float64
	GPUPowerW       float64
	GPUSMClockMHz   float64
	GPUMemClockMHz  float64
	GPUThrottle     string
}

// Sample collects a telemetry snapshot for the detected GPU. CPU and RAM come
// from gopsutil; GPU values come from the platform collector in telemetry_*.go
// (nvidia-smi or sysfs on Linux, nvidia-smi on Windows, unavailable on macOS
// without root). Safe to call from any goroutine; every probe degrades to zero
// instead of failing the snapshot.
func Sample(gpu *GPU) Metrics {
	var m Metrics
	interval := time.Duration(0)
	if !cpuPrimed {
		interval = 100 * time.Millisecond
	}
	if pcts, err := cpu.Percent(interval, false); err == nil && len(pcts) > 0 {
		m.CPUPercent = pcts[0]
		m.CPUPercentKnown = true
	}
	cpuPrimed = true
	if vm, err := mem.VirtualMemory(); err == nil && vm.Total > 0 {
		m.MemTotalGB = float64(vm.Total) / (1 << 30)
		m.MemUsedGB = float64(vm.Total-vm.Available) / (1 << 30)
	}
	m.CPUTempC = cpuTempC()
	sampleGPU(gpu, &m)
	return m
}
