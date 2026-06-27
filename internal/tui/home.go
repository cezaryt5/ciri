package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type homeModel struct {
	cursor int
	items  []homeItem
}

type homeItem struct {
	label  string
	target screen
}

func newHomeModel() *homeModel {
	return &homeModel{
		items: []homeItem{
			{label: "Explore Models", target: screenExplore},
			{label: "Download Models", target: screenDownload},
			{label: "Manage Local LLMs", target: screenLocal},
			{label: "Settings / Hardware Configs", target: screenSettings},
		},
	}
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

func (h *homeModel) homeView(a *App) string {
	var b strings.Builder

	b.WriteString("\n  What do you want to do?\n\n")

	for i, item := range h.items {
		if h.cursor == i {
			line := fmt.Sprintf(" \u25b6 %s", item.label)
			b.WriteString(SelectedRow.Render(line) + "\n")
		} else {
			line := fmt.Sprintf("   %s", item.label)
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n" + Footer.Render("  \u2191\u2193 Select  Enter Confirm  q Quit"))
	return b.String()
}
