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
	var b strings.Builder

	b.WriteString("\n\n  Hardware Config\n\n")

	gpuName := "Unknown"
	vramGB := 0.0
	if s.gpu != nil {
		gpuName = s.gpu.Name
		vramGB = s.gpu.VRAMGB
	}

	cpuModel := s.specs.CpuModel
	if cpuModel == "" {
		cpuModel = "Unknown"
	}

	ollamaStr := "\u2713"
	if !s.specs.HasOllama {
		ollamaStr = "\u00d7"
	}
	llamaStr := "\u2713"
	if !s.specs.HasLlamaCPP {
		llamaStr = "\u00d7"
	}

	b.WriteString(fmt.Sprintf("  %-18s %s\n", "GPU:", gpuName))
	b.WriteString(fmt.Sprintf("  %-18s %.1f GB\n", "VRAM:", vramGB))
	b.WriteString(fmt.Sprintf("  %-18s %.1f / %.1f GB\n", "RAM:", s.specs.RamAvailGB, s.specs.RamTotalGB))
	b.WriteString(fmt.Sprintf("  %-18s %s (%dc)\n", "CPU:", cpuModel, s.specs.CpuCores))
	b.WriteString(fmt.Sprintf("  %-18s %s\n", "Ollama:", ollamaStr))
	b.WriteString(fmt.Sprintf("  %-18s %s\n", "llama.cpp:", llamaStr))

	b.WriteString("\n" + Footer.Render("  Esc Back"))
	return b.String()
}
