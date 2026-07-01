package tui

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// Screen owns the terminal lifecycle: alternate screen buffer, raw mode, cursor
// visibility, size queries, and idempotent restoration. Using the alternate
// buffer means the dashboard never pollutes scrollback and the user's prior
// content + scroll position are restored on exit.
type Screen struct {
	out     *os.File
	fd      int
	raw     *term.State
	cleaned bool
}

const (
	enterAlt   = "\x1b[?1049h"
	leaveAlt   = "\x1b[?1049l"
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
	home       = "\x1b[H"
	eraseLine  = "\x1b[K"
	eraseBelow = "\x1b[J"
)

// NewScreen enters the alternate screen, hides the cursor, and puts the
// terminal into raw mode. Call Close (deferred) to restore.
func NewScreen() (*Screen, error) {
	fd := int(os.Stdin.Fd())
	st, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	s := &Screen{out: os.Stdout, fd: fd, raw: st}
	s.write(enterAlt + hideCursor)
	return s, nil
}

// Size returns the current terminal dimensions, freshly queried so resizes are
// always picked up. Falls back to 80x24 if the query fails.
func (s *Screen) Size() (w, h int) {
	w, h, err := term.GetSize(s.fd)
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// Draw paints a frame given as already-laid-out lines. It homes the cursor,
// erases each line's tail, then erases anything below the last line so a
// shorter frame leaves no residue. No full clear -> no flicker.
func (s *Screen) Draw(lines []string) {
	var b strings.Builder
	b.WriteString(home)
	for i, line := range lines {
		b.WriteString(line)
		b.WriteString(eraseLine)
		if i < len(lines)-1 {
			b.WriteString("\r\n")
		}
	}
	b.WriteString(eraseBelow)
	s.write(b.String())
}

func (s *Screen) write(str string) { _, _ = s.out.WriteString(str) }

// Close restores the terminal. Safe to call more than once.
func (s *Screen) Close() {
	if s.cleaned {
		return
	}
	s.cleaned = true
	if s.raw != nil {
		_ = term.Restore(s.fd, s.raw)
	}
	s.write(showCursor + leaveAlt)
}
