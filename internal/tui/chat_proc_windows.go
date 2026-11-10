//go:build windows

package tui

import "os/exec"

func setChatProcessGroup(cmd *exec.Cmd) {}

func killChatProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
