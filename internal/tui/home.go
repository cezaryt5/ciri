package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

type homeModel struct {
	cursor int
	items  []homeItem
}

type homeItem struct {
	label  string
	desc   string
	target screen
}

func newHomeModel() *homeModel {
	return &homeModel{
		items: []homeItem{
			{label: "Chat", desc: "LOCAL MODEL + ANALYSIS", target: screenChat},
			{label: "Explore Models", desc: "BROWSE THE CATALOG", target: screenExplore},
			{label: "Downloads", desc: "DOWNLOAD MANAGER", target: screenDownload},
			{label: "Manage Local LLMs", desc: "LOCAL RUNTIMES", target: screenLocal},
			{label: "Settings / Hardware", desc: "SYSTEM PROFILE", target: screenSettings},
		},
	}
}

// ciriHero is the landing wordmark. The hero is intentionally the loudest
// thing on the home screen, echoing the opencode-style landing page, while
// every other screen stays data-first.
var ciriHero = []string{
	" ██████╗ ██╗██████╗ ██╗",
	"██╔════╝ ██║██╔══██╗██║",
	"██║      ██║██████╔╝██║",
	"██║      ██║██╔══██╗██║",
	"╚██████╗ ██║██║  ██║██║",
	" ╚═════╝ ╚═╝╚═╝  ╚═╝╚═╝",
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
		if h.cursor < len(h.items)-1 {
			h.cursor++
		}
	case "enter", " ":
		if h.cursor >= 0 && h.cursor < len(h.items) {
			item := h.items[h.cursor]
			if item.target == screenExplore {
				a.explore = nil
			}
			return func() tea.Msg { return navigateMsg{target: item.target} }
		}
	}
	return nil
}

// homeScreenView is the landing screen: centered wordmark, tagline, command
// menu and an opencode-style status strip.
func (h *homeModel) homeScreenView(a *App, width int) string {
	height := a.height
	if height <= 0 {
		height = 24
	}

	menuW := width - 10
	if menuW > 64 {
		menuW = 64
	}
	if menuW < 40 {
		menuW = 40
	}
	if menuW > width-2 {
		menuW = width - 2
	}

	var block []string
	if height >= 18 && width >= 44 {
		for _, line := range ciriHero {
			block = append(block, Wordmark.Render(strings.TrimRight(line, " ")))
		}
		block = append(block, "")
	}
	block = append(block,
		StatusOn.Render("\u25a0")+"  "+Tagline.Render("CAN I RUN IT?"),
		"",
	)
	block = append(block, strings.Split(RenderPanel("Command", h.homeMenuContent(menuW-2), menuW), "\n")...)
	block = append(block,
		"",
		renderHints(width, keyHint{"\u2191\u2193", "move"}, keyHint{"enter", "open"}, keyHint{"q", "quit"}),
	)

	// Keep one blank spacer plus the status strip below the hero, then
	// center the block in the remaining rows.
	avail := height - 2
	if avail < 1 {
		avail = 1
	}
	top := 0
	if len(block) < avail {
		top = (avail - len(block)) / 2
	}

	lines := make([]string, 0, height)
	for i := 0; i < top; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, block...)
	for len(lines) < avail {
		lines = append(lines, "")
	}
	lines = append(lines, "", homeStatusLine(a, width))
	if len(lines) > height {
		lines = lines[:height]
	}

	for i := 0; i < len(lines)-1; i++ {
		lines[i] = centerRendered(lines[i], width)
	}
	return strings.Join(lines, "\n")
}

// homeMenuContent renders the operation list for a panel of inner width.
func (h *homeModel) homeMenuContent(panelInner int) string {
	contentW := panelInner - 2
	if contentW < 22 {
		contentW = 22
	}

	nameW := 26
	switch {
	case contentW < 48:
		nameW = 16
	case contentW < 58:
		nameW = 20
	case contentW < 66:
		nameW = 24
	}
	showDesc := contentW >= 46

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + Label.Render("SELECT AN OPERATION") + "\n")
	b.WriteString("\n")

	for i, item := range h.items {
		marker := " "
		if h.cursor == i {
			marker = "\u258c"
		}
		name := padRight(truncate(item.label, nameW), nameW)
		prefix := fmt.Sprintf("  %s %02d  ", marker, i+1)
		row := prefix + name

		descW := contentW - lipgloss.Width(row) - 2
		desc := ""
		if showDesc && descW >= 8 {
			desc = truncate(item.desc, descW)
			row += "  " + desc
		}

		if h.cursor == i {
			selectedText := fmt.Sprintf("%02d  ", i+1) + name
			if desc != "" {
				selectedText += "  " + desc
			}
			prefix := "  " + Cursor.Render("\u258c") + " "
			restW := contentW - lipgloss.Width(prefix)
			if restW < 0 {
				restW = 0
			}
			b.WriteString(prefix + SelectedRow.Width(restW).MaxWidth(restW).Render(selectedText) + "\n")
			continue
		}

		var line strings.Builder
		line.WriteString("  ")
		line.WriteString(Faint.Render(marker))
		line.WriteString(" ")
		line.WriteString(Label.Render(fmt.Sprintf("%02d", i+1)))
		line.WriteString("  ")
		line.WriteString(Value.Render(name))
		if desc != "" {
			line.WriteString("  ")
			line.WriteString(Tagline.Render(desc))
		}
		b.WriteString(line.String() + "\n")
	}

	b.WriteString("\n")
	return b.String()
}

// homeStatusLine is the opencode-style bottom strip: hardware state on the
// left, build identity on the right. Live telemetry (load, VRAM, temps) is
// injected when the sampler has data; static facts remain the fallback.
func homeStatusLine(a *App, width int) string {
	var left strings.Builder
	m := a.metrics
	if a.gpu != nil && a.gpu.Name != "" {
		left.WriteString(StatusOn.Render("\u25a0") + " " + Label.Render("GPU") + "  " +
			Value.Render(a.gpu.Name))
		if a.gpu.VRAMGB > 0 {
			left.WriteString("  " + Tagline.Render(fmt.Sprintf("%.1f GB", a.gpu.VRAMGB)))
		}
		if m.GPUUtilKnown {
			left.WriteString("  " + ValueDim.Render(fmt.Sprintf("%.0f%%", m.GPUUtilPct)))
		}
		if m.GPUTempC > 0 {
			left.WriteString("  " + tempValue(m.GPUTempC).Render(fmt.Sprintf("%.0f\u00b0C", m.GPUTempC)))
		}
	} else {
		left.WriteString(StatusAlert.Render("\u25a0") + " " + StatusAlert.Render("NO GPU DETECTED"))
	}
	ramTotal, ramAvail := a.specs.RamTotalGB, a.specs.RamAvailGB
	if m.MemTotalGB > 0 {
		ramTotal = m.MemTotalGB
		ramAvail = ramTotal - m.MemUsedGB
	}
	if ramTotal > 0 {
		left.WriteString("   " + Label.Render("RAM") + "  " +
			Value.Render(fmt.Sprintf("%.1f/%.1f GB", ramAvail, ramTotal)))
	}
	if m.CPUPercentKnown {
		left.WriteString("   " + Label.Render("CPU") + "  " +
			Value.Render(fmt.Sprintf("%.0f%%", m.CPUPercent)))
	}
	if m.CPUTempC > 0 {
		left.WriteString("  " + tempValue(m.CPUTempC).Render(fmt.Sprintf("%.0f\u00b0C", m.CPUTempC)))
	}
	left.WriteString("   " + Label.Render("OLLAMA") + "  " + statusSquare(a.specs.HasOllama))
	left.WriteString("   " + Label.Render("LLAMA.CPP") + "  " + statusSquare(a.specs.HasLlamaCPP))

	right := Tagline.Render("CIRI")
	if v := versionLabel(a.version); v != "" {
		right = Tagline.Render("CIRI " + v)
	}
	return "  " + alignLine(left.String(), right, width-4) + "  "
}

// centerRendered pads a rendered line with spaces until it sits in the
// middle of width. ANSI-aware via lipgloss.Width.
func centerRendered(line string, width int) string {
	w := lipgloss.Width(line)
	if width <= 0 || w >= width {
		return line
	}
	left := (width - w) / 2
	return strings.Repeat(" ", left) + line
}
