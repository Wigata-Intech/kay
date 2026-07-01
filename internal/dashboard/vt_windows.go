//go:build windows

package dashboard

import "golang.org/x/sys/windows"

// enableVT turns on ANSI/virtual-terminal processing for the Windows console so
// escape sequences render (needed on legacy conhost; Windows Terminal already
// enables it). Best-effort: failures are ignored.
func enableVT() {
	handle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return
	}
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
