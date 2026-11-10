package tui

import (
	"fmt"
	"strings"

	"github.com/cezaryt5/ciri/internal/hardware"

	tea "github.com/charmbracelet/bubbletea"
)

type settingsModel struct {
	specs hardware.Specs
	gpu   *hardware.GPU
}

func (s *settingsModel) settingsUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return func() tea.Msg { return navigateMsg{target: screenHome} }
	}
	return nil
}

func (s *settingsModel) settingsView(a *App) string {
	inner := a.width - 4
	if inner < 22 {
		inner = 22
	}

	var b strings.Builder

	gpuName := "UNKNOWN"
	vram := "\u2014"
	gpuClass := ""
	if s.gpu != nil {
		gpuName = s.gpu.Name
		if s.gpu.VRAMGB > 0 {
			vram = fmt.Sprintf("%.1f GB", s.gpu.VRAMGB)
		}
		gpuClass = s.gpu.Architecture
	}

	cpuModel := strings.TrimSpace(s.specs.CpuModel)
	if cpuModel == "" {
		cpuModel = "UNKNOWN"
	}
	cores := "\u2014"
	if s.specs.CpuCores > 0 {
		cores = fmt.Sprintf("%d", s.specs.CpuCores)
	}

	b.WriteString("\n")
	b.WriteString(RenderSection("System Profile", inner) + "\n")
	b.WriteString("  " + field("GPU", gpuName, 12) + "\n")
	b.WriteString("  " + field("VRAM", vram, 12) + "\n")
	if gpuClass != "" {
		b.WriteString("  " + field("GPU Class", gpuClass, 12) + "\n")
	}
	b.WriteString("  " + field("Memory", fmt.Sprintf("%.1f / %.1f GB", s.specs.RamAvailGB, s.specs.RamTotalGB), 12) + "\n")
	b.WriteString("  " + field("CPU", cpuModel, 12) + "\n")
	b.WriteString("  " + field("Cores", cores, 12) + "\n")

	b.WriteString("\n")
	b.WriteString(RenderSection("Runtimes", inner) + "\n")
	b.WriteString(runtimeLine("OLLAMA", s.specs.HasOllama) + "\n")
	b.WriteString(runtimeLine("LLAMA.CPP", s.specs.HasLlamaCPP) + "\n")

	return b.String()
}

func runtimeLine(name string, on bool) string {
	state := "NOT FOUND"
	nameStyle := Tagline
	if on {
		state = "DETECTED"
		nameStyle = Value
	}
	return "  " + statusSquare(on) + " " + padRight(nameStyle.Render(name), 14) +
		Tagline.Render(state)
}
