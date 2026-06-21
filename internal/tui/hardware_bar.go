package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/cezaryt5/Can_I_Run_IT/internal/hardware"
)

// RenderHardwareBar — internal/tui/hardware_bar.go:11
// Called from: app.go:116 (in App.View)
// Renders a one-line hardware status bar showing CPU model (truncated 20),
// RAM usage (avail/total), and GPU name + VRAM. Components separated by
// "│" pipe characters. Padded to full terminal width.
func RenderHardwareBar(specs hardware.Specs, gpu *hardware.GPU, width int) string {
	var parts []string

	cpu := specs.CpuModel
	if len(cpu) > 20 {
		cpu = cpu[:20]
	}
	parts = append(parts, cpu)

	ram := fmt.Sprintf("%.1f/%.1f GB RAM", specs.RamAvailGB, specs.RamTotalGB)
	parts = append(parts, ram)

	if gpu != nil && gpu.VRAMGB > 0 {
		gpuStr := fmt.Sprintf("%s %.0fGB", gpu.Name, gpu.VRAMGB)
		if len(gpuStr) > 30 {
			gpuStr = gpuStr[:30]
		}
		parts = append(parts, gpuStr)
	} else if gpu != nil {
		gpuStr := gpu.Name
		if len(gpuStr) > 25 {
			gpuStr = gpuStr[:25]
		}
		parts = append(parts, gpuStr)
	} else {
		parts = append(parts, specs.RawGPUName)
	}

	raw := " " + strings.Join(parts, " \u2502 ") + " "

	if width > 0 && width > lipgloss.Width(raw) {
		return raw + strings.Repeat(" ", width-lipgloss.Width(raw))
	}
	return raw
}
