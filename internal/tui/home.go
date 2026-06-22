package tui

import (
	"fmt"
	"strings"

	"github.com/cezaryt5/ciri/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var ciriLogo = []string{
	`   ██████╗ ██╗██████╗ ██╗`,
	`  ██╔════╝ ██║██╔══██╗██║`,
	`  ██║      ██║██████╔╝██║`,
	`  ██║      ██║██╔══██╗██║`,
	`  ╚██████╗ ██║██║  ██║██║`,
	`   ╚═════╝ ╚═╝╚═╝  ╚═╝╚═╝`,
}

var (
	arrowStyle = lipgloss.NewStyle().Foreground(cyan)
	checkStyle = lipgloss.NewStyle().Foreground(green)
	crossStyle = lipgloss.NewStyle().Foreground(red)
)

type homeModel struct {
	cursor int
}

func (h *homeModel) homeUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "up", "k":
		if h.cursor > 0 {
			h.cursor--
		}
	case "down", "j":
		if h.cursor < len(model.AllCategories())-1 {
			h.cursor++
		}
	case "enter", " ":
		cats := model.AllCategories()
		if h.cursor >= 0 && h.cursor < len(cats) {
			a.category = cats[h.cursor]
			a.results = nil
			return func() tea.Msg { return navigateMsg{target: screenResults} }
		}
	}
	return nil
}

func (h *homeModel) homeView(a *App) string {
	var b strings.Builder

	b.WriteString(renderHeader(a))
	b.WriteString("\n\n")

	b.WriteString("  What do you want to do?\n\n")

	cats := model.AllCategories()
	for i, cat := range cats {
		count := a.counts[cat]
		if h.cursor == i {
			line := fmt.Sprintf(" \u25b6 %-14s (%d models fit)", cat, count)
			b.WriteString(SelectedRow.Render(line) + "\n")
		} else {
			line := fmt.Sprintf("   %-14s (%d models fit)", cat, count)
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n" + Footer.Render("  ↑↓ Select  Enter Confirm  q Quit"))
	return b.String()
}

func renderHeader(a *App) string {
	info := hardwareInfoLines(a)
	var b strings.Builder
	for i, logoLine := range ciriLogo {
		b.WriteString("  ")
		b.WriteString(logoLine)
		b.WriteString("   ")
		b.WriteString(info[i])
		if i < len(ciriLogo)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func hardwareInfoLines(a *App) []string {
	gpuName := "Unknown"
	vramGB := 0.0
	if a.gpu != nil {
		gpuName = a.gpu.Name
		vramGB = a.gpu.VRAMGB
	}

	cpuModel := a.specs.CpuModel
	if cpuModel == "" {
		cpuModel = "Unknown"
	}

	arrow := arrowStyle.Render(">")
	check := checkStyle.Render("\u2713")
	cross := crossStyle.Render("\u00d7")

	ollamaStr := cross
	if a.specs.HasOllama {
		ollamaStr = check
	}
	llamaStr := cross
	if a.specs.HasLlamaCPP {
		llamaStr = check
	}

	return []string{
		fmt.Sprintf("%s GPU:    %s",             arrow, gpuName),
		fmt.Sprintf("%s VRAM:   %.1f GB",        arrow, vramGB),
		fmt.Sprintf("%s RAM:    %.1f / %.1f GB", arrow, a.specs.RamAvailGB, a.specs.RamTotalGB),
		fmt.Sprintf("%s CPU:    %s (%dc)",       arrow, cpuModel, a.specs.CpuCores),
		fmt.Sprintf("%s Ollama: %s",              arrow, ollamaStr),
		fmt.Sprintf("%s llama.cpp: %s",           arrow, llamaStr),
	}
}
