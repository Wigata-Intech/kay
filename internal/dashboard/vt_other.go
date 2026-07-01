//go:build !windows

package dashboard

// enableVT is a no-op on non-Windows platforms, where terminals already handle
// ANSI escape sequences.
func enableVT() {}
