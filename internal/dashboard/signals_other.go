//go:build !unix

package dashboard

import (
	"os"
	"os/signal"
)

// watchSignals on non-unix platforms only listens for interrupt; there is no
// SIGWINCH, so resizes are picked up lazily by the per-frame size query.
func watchSignals() chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	return ch
}

// signalIsQuit treats any delivered signal as a request to quit.
func signalIsQuit(os.Signal) bool { return true }

func stopSignals(ch chan os.Signal) { signal.Stop(ch) }
