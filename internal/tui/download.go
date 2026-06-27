package tui

import tea "github.com/charmbracelet/bubbletea"

type downloadModel struct{}

func (dm *downloadModel) downloadUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return func() tea.Msg { return navigateMsg{target: screenHome} }
	}
	return nil
}

func (dm *downloadModel) downloadView(a *App) string {
	return "\n\n  Download Models\n\n  Coming soon.\n\n" +
		Footer.Render("  Esc Back")
}
