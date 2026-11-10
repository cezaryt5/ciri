package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cezaryt5/ciri/internal/hardware"
)

// renderChrome draws the persistent brand + hardware telemetry strip.
// It is a Level 1 container: sharp frame, crosshair corners, mono labels
// and stark-white values.
func renderChrome(a *App, width int) string {
	inner := width - 2

	brand := Wordmark.Render("CIRI") + "  " +
		StatusOn.Render("\u25a0") + "  " +
		Tagline.Render("CAN I RUN IT?")

	version := ""
	if v := versionLabel(a.version); v != "" {
		version = Tagline.Render(v)
	}

	lines := []string{
		"  " + chromeColumns(gpuStat(a), runtimesStat(a.specs), inner-4),
		"  " + chromeColumns(cpuStat(a), memoryStat(a), inner-4),
	}
	return renderFrame(brand, version, lines, width)
}

// tempValue styles a temperature reading: neutral while cool, white when
// warm, signal red when hot.
func tempValue(c float64) lipgloss.Style {
	switch {
	case c >= 85:
		return StatusAlert
	case c >= 70:
		return Value
	default:
		return Tagline
	}
}

func gpuStat(a *App) string {
	label := Label.Render("GPU") + "  "
	if a.gpu == nil || a.gpu.Name == "" {
		return label + StatusAlert.Render("\u25a0") + " " + StatusAlert.Render("NO GPU DETECTED")
	}
	out := StatusOn.Render("\u25a0") + " " + Value.Render(a.gpu.Name)
	m := a.metrics
	if m.GPUUtilKnown {
		out += "  " + ValueDim.Render(fmt.Sprintf("%.0f%%", m.GPUUtilPct))
	}
	vramTotal := a.gpu.VRAMGB
	if m.VRAMTotalGB > 0 {
		vramTotal = m.VRAMTotalGB
	}
	switch {
	case m.VRAMUsedGB > 0 && vramTotal > 0:
		out += "  " + Tagline.Render(fmt.Sprintf("%.1f/%.0f GB", m.VRAMUsedGB, vramTotal))
	case vramTotal > 0:
		out += "  " + Tagline.Render(fmt.Sprintf("%.1f GB VRAM", vramTotal))
	}
	if m.GPUTempC > 0 {
		out += "  " + tempValue(m.GPUTempC).Render(fmt.Sprintf("%.0f\u00b0C", m.GPUTempC))
	}
	if m.GPUPowerW > 0 {
		out += "  " + Tagline.Render(fmt.Sprintf("%.0fW", m.GPUPowerW))
	}
	return label + out
}

func cpuStat(a *App) string {
	cpu := strings.TrimSpace(a.specs.CpuModel)
	if cpu == "" {
		cpu = "UNKNOWN"
	}
	out := Label.Render("CPU") + "  " + Value.Render(cpu)
	if cores := coresLabel(a.specs.CpuCores); cores != "" {
		out += "  " + Tagline.Render(cores)
	}
	m := a.metrics
	if m.CPUPercentKnown {
		out += "  " + ValueDim.Render(fmt.Sprintf("%.0f%%", m.CPUPercent))
	}
	if m.CPUTempC > 0 {
		out += "  " + tempValue(m.CPUTempC).Render(fmt.Sprintf("%.0f\u00b0C", m.CPUTempC))
	}
	return out
}

func coresLabel(cores int64) string {
	if cores <= 0 {
		return ""
	}
	return fmt.Sprintf("%dC", cores)
}

func runtimesStat(specs hardware.Specs) string {
	return Label.Render("RUNTIMES") + "  " +
		runtimeBadge("OLLAMA", specs.HasOllama) + "  " +
		runtimeBadge("LLAMA.CPP", specs.HasLlamaCPP)
}

func memoryStat(a *App) string {
	total := a.specs.RamTotalGB
	avail := a.specs.RamAvailGB
	if a.metrics.MemTotalGB > 0 {
		total = a.metrics.MemTotalGB
		avail = total - a.metrics.MemUsedGB
	}

	if total <= 0 {
		value := "\u2014"
		if avail > 0 {
			value = fmt.Sprintf("%.1f GB FREE", avail)
		}
		return Label.Render("MEMORY") + "  " + Value.Render(value)
	}

	usedPct := (total - avail) / total * 100
	if usedPct < 0 {
		usedPct = 0
	}

	value := fmt.Sprintf("%.1f / %.1f GB", avail, total)
	style := Value
	if usedPct >= 90 {
		style = StatusAlert
	}
	return Label.Render("MEMORY") + "  " + style.Render(value) + "  " +
		Tagline.Render(fmt.Sprintf("%.0f%% USED", usedPct))
}

func runtimeBadge(name string, on bool) string {
	if on {
		return StatusOn.Render("\u25a0") + " " + Value.Render(name)
	}
	return StatusOff.Render("\u25a0") + " " + Tagline.Render(name)
}

// chromeColumns lays out a telemetry pair: left-aligned primary data and
// right-aligned secondary data, collapsing the right column when cramped.
func chromeColumns(left, right string, inner int) string {
	if inner < 1 {
		return left
	}
	rw := lipgloss.Width(right)
	if right == "" || rw > inner-14 {
		return truncate(left, inner)
	}

	leftW := inner - rw - 2
	if leftW < 8 {
		return truncate(left, inner)
	}
	left = padRight(truncate(left, leftW), leftW)
	return left + "  " + right
}
