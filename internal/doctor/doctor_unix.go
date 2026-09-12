//go:build unix

package doctor

import (
	"os/exec"
	"syscall"
)

// setProcAttrs puts the probe in its own process group so cancellation can
// kill the whole group, including any children the probe spawns.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProc kills the probe's whole process group.
func killProc(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
