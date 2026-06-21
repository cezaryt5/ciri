//go:build !windows

package hardware

import (
	"context"
	"os/exec"
	"time"
)

// execWithTimeout runs a command with a deadline. Returns stdout on success.
func execWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cmdCtx, name, args...).Output()
}

// execLookPath returns the absolute path to an executable, or "" if not found.
func execLookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}
