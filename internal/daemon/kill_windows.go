package daemon

import (
	"os/exec"
	"syscall"
)

// groupAttr is the command's process attributes: a group of its own,
// whose group a stop ends.
func groupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessGroup ends the command: Windows kills no group as a whole.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.Process.Kill()
}
