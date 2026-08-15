//go:build unix

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// watchResize invokes onResize on every terminal-size change (SIGWINCH) until
// stop is closed.
func watchResize(onResize func(), stop <-chan struct{}) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-stop:
				return
			case <-ch:
				onResize()
			}
		}
	}()
}
