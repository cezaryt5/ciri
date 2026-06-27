package tui

import tea "github.com/charmbracelet/bubbletea"

type localModel struct{}

func (lm *localModel) localUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return func() tea.Msg { return navigateMsg{target: screenHome} }
	}
	return nil
}

func (lm *localModel) localView(a *App) string {
	return "\n\n  Manage Local LLMs\n\n  Coming soon.\n\n" +
		Footer.Render("  Esc Back")
}
