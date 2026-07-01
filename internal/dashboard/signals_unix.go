//go:build unix

package dashboard

import (
	"os"
	"os/signal"
	"syscall"
)

// watchSignals delivers terminal-resize (SIGWINCH) and termination (SIGTERM)
// signals so the dashboard can redraw on resize and clean up on exit.
func watchSignals() chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH, syscall.SIGTERM)
	return ch
}

// signalIsQuit reports whether the signal should end the dashboard.
func signalIsQuit(s os.Signal) bool { return s == syscall.SIGTERM }

func stopSignals(ch chan os.Signal) { signal.Stop(ch) }
