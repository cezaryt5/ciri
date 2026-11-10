package tui

import (
	"fmt"
	"os"
)

const (
	// OSC 11 sets the terminal's default background color. Inner ANSI style
	// resets fall back to this color, so it is the only way to make the whole
	// window black — including rows/cells not painted by the app.
	setBlackBackground = "\x1b]11;rgb:0000/0000/0000\x1b\\"
	// OSC 111 restores the terminal's configured background color.
	resetBackground = "\x1b]111\x1b\\"
)

// BlackTerminalBackground sets the terminal's default background to black for
// the lifetime of the TUI. The returned function restores the user's terminal
// background. Without this, ANSI resets inside styled lines fall back to the
// terminal theme color and leave the canvas looking non-black.
func BlackTerminalBackground() func() {
	if !stdoutIsTerminal() {
		return func() {}
	}
	fmt.Fprint(os.Stdout, setBlackBackground)
	return func() {
		fmt.Fprint(os.Stdout, resetBackground)
	}
}

func stdoutIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
