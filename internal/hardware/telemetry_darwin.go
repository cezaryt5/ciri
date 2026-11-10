//go:build darwin

package hardware

// sampleGPU is a no-op on macOS: GPU utilization lives behind powermetrics,
// which requires root. The chrome renders GPU load fields as dashes; CPU and
// RAM (including unified memory) still tick from gopsutil.
func sampleGPU(gpu *GPU, m *Metrics) {}

// cpuTempC is not readable without privileged SMC access; zero hides it.
func cpuTempC() float64 {
	return 0
}
