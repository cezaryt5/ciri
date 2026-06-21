package tui

import (
	"fmt"
	"strings"

	"github.com/cezaryt5/Can_I_Run_IT/internal/model"

	tea "github.com/charmbracelet/bubbletea"
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
			// Wipe the old results so App.View() builds a fresh one for the new category
			a.results = nil
			return func() tea.Msg { return navigateMsg{target: screenResults} }
		}
	}
	return nil
}

func (h *homeModel) homeView(a *App) string {
	var b strings.Builder

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
