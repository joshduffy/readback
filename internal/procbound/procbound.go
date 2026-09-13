// Package procbound builds exec.Cmd values that are hard-bounded by their context.
package procbound

import (
	"context"
	"os/exec"
	"time"
)

// Command returns a command that is hard-bounded by ctx. On unix it runs in
// its own process group and cancellation kills the whole group, so a program
// that spawns children still returns within the deadline; on Windows
// cancellation kills only the direct process. WaitDelay caps the time spent
// waiting on pipes inherited by grandchildren.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	setProcAttrs(cmd)
	cmd.Cancel = func() error {
		return killProc(cmd)
	}
	cmd.WaitDelay = 500 * time.Millisecond
	return cmd
}
