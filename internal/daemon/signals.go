//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// ignoreSignals are the signals kerod ignores: a hangup, as its terminal
// ends, and a broken pipe, which a client's socket takes.
func ignoreSignals() [][]os.Signal { return [][]os.Signal{{syscall.SIGHUP, syscall.SIGPIPE}} }

// stopSignals is the channel the signals that stop kerod come on, and
// stopSignal the one that a client sends when it replaces it.
func stopSignals() chan os.Signal { return make(chan os.Signal, 1) }

func stopSignal() os.Signal { return syscall.SIGTERM }
