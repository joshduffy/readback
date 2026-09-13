//go:build unix

package procbound

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A cancelled command must take its grandchildren with it: the shell backgrounds a
// sleep that inherits stdout, prints its pid, then blocks. Without the process-group
// kill the sleep survives and Wait would hang on the inherited pipe.
func TestCancelKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cmd := Command(ctx, "sh", "-c", "sleep 30 & echo $!; wait")
	var out bytes.Buffer
	cmd.Stdout = &out
	start := time.Now()
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected the deadline to fail the command")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Run took %s, not bounded by the deadline", elapsed)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(out.String()))
	if convErr != nil {
		t.Fatalf("grandchild pid not captured: %q", out.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("grandchild %d survived cancellation", pid)
}
