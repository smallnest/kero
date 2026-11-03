package daemon

import "os"

// ignoreSignals are the signals kerod ignores: none, on Windows.
func ignoreSignals() [][]os.Signal { return nil }

// stopSignals are the channel the signals that stop kerod come on, and
// stopSignal the one that a client sends when it replaces it: an
// interrupt, which the stop event gives (notifyStop).
func stopSignals() chan os.Signal { return make(chan os.Signal, 1) }

func stopSignal() os.Signal { return os.Interrupt }
