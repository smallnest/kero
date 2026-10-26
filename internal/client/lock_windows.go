package client

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes the lock of f without waiting, failing when another
// process has it: the daemon's (daemon.lockFile).
func lockFile(f *os.File) error {
	var o windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o)
}

// UnlockFile lets go of f's lock.
func UnlockFile(f *os.File) {
	var o windows.Overlapped
	windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o)
}

// Running reports whether the process of pid exists.
func Running(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return true // another user's, or the system's: it is running
	}
	if err != nil {
		return false // ERROR_INVALID_PARAMETER: no such process
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err != nil || ev == uint32(windows.WAIT_TIMEOUT)
}
