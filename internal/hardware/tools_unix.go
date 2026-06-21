//go:build !windows

package hardware

import (
	"context"
	"os/exec"
	"time"
)

// execWithTimeout — internal/hardware/tools_unix.go:12
// Called from: detection_linux.go:88,134,144; detection_darwin.go:29
// Runs an external command with a context deadline. Returns stdout bytes on
// success, or an error if the command times out or fails.
func execWithTimeout(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(cmdCtx, name, args...).Output()
}

// execLookPath — internal/hardware/tools_unix.go:19
// Called from: detection.go:126,128,129 (in DetectOllamaCpp)
// Returns the absolute path of an executable via exec.LookPath. Returns ""
// if the executable is not found on PATH.
func execLookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}
