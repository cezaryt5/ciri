package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type localModel struct{}

func (lm *localModel) localUpdate(a *App, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return func() tea.Msg { return navigateMsg{target: screenHome} }
	}
	return nil
}

func (lm *localModel) localView(a *App) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + Label.Render("STATUS") + "  " + statusSquare(false) + " " +
		Value.Render("NOT AVAILABLE") + "\n")
	b.WriteString("\n")
	b.WriteString("  " + Value.Render("Runtime management is not wired into this build.") + "\n")
	b.WriteString("  " + Value.Render("Detected runtimes appear in the header strip.") + "\n")
	return b.String()
}
