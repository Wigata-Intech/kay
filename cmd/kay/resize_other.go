//go:build !unix

package main

// watchResize is a no-op on platforms without SIGWINCH; the remote PTY keeps
// the size negotiated at session start.
func watchResize(func(), <-chan struct{}) {}
