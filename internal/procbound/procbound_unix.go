//go:build unix

package procbound

import (
	"os/exec"
	"syscall"
)

// setProcAttrs puts the process in its own process group so cancellation can
// kill the whole group, including any children the process spawns.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProc kills the process's whole process group.
func killProc(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
