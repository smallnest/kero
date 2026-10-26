//go:build !windows

package client

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// lockFile takes the lock of f without waiting, failing when another
// process has it. It is flock's: a lock another process of this one holds,
// as one it forked, is its too.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// UnlockFile lets go of f's lock.
func UnlockFile(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// Running reports whether the process of pid exists, as one that is
// stopped does, waiting for a signal to go on: a zombie, of a parent that
// reaps no child, does not. A signal says no of one, or of a process user
// Debugger may not signal, as a developer tool's: ps tells.
func Running(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && err != syscall.EPERM {
		return false
	}
	b, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	stat := strings.TrimSpace(string(b)) // "" of a zombie
	return stat != "" && !strings.HasPrefix(stat, "Z")
}
