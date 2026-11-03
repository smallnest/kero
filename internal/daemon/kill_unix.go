//go:build unix

package daemon

import (
	"os/exec"
	"syscall"
)

// groupAttr is the command's process attributes: a session of its own,
// whose group a stop ends.
func groupAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// killProcessGroup ends the command and what it started: its process
// group, which groupAttr makes, or the command alone.
func killProcessGroup(cmd *exec.Cmd) {
	if syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) != nil {
		cmd.Process.Kill()
	}
}
