//go:build unix && !darwin

package daemon

import "syscall"

// groupAttr is the command's process attributes: a session of its own,
// whose group a stop ends.
func groupAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
