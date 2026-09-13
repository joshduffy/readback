//go:build windows

package procbound

import "os/exec"

// Windows has no unix-style process groups; set nothing. WaitDelay in
// Command still caps the wait on inherited pipes.
func setProcAttrs(cmd *exec.Cmd) {}

// killProc kills only the direct process.
func killProc(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
