package tui

import (
	"os"
	"unicode/utf8"
)

// EventType classifies a loop event.
type EventType int

const (
	EventKey EventType = iota
	EventQuit
)

// Key names non-rune keys.
type Key int

const (
	KeyRune Key = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyTab
	KeyShiftTab
	KeyEnter
	KeyEsc
	KeyBackspace
	KeyPgUp
	KeyPgDn
	KeyHome
	KeyEnd
	KeyCtrlD
	KeyCtrlU
)

// Event is a decoded input event.
type Event struct {
	Type EventType
	Key  Key
	Rune rune
}

// Reader decodes terminal input into Events. In raw mode an escape sequence is
// usually delivered in a single Read, which this relies on; any leftover bytes
// are buffered for the next call.
type Reader struct {
	in  *os.File
	buf []byte
}

// NewReader builds a Reader over the given file (normally os.Stdin).
func NewReader(in *os.File) *Reader { return &Reader{in: in} }

// ReadEvent blocks until one event can be decoded.
func (r *Reader) ReadEvent() (Event, error) {
	for {
		if len(r.buf) > 0 {
			ev, n := Decode(r.buf)
			if n > 0 {
				r.buf = r.buf[n:]
				return ev, nil
			}
		}
		tmp := make([]byte, 32)
		n, err := r.in.Read(tmp)
		if n > 0 {
			r.buf = append(r.buf, tmp[:n]...)
		}
		if err != nil {
			return Event{Type: EventQuit}, err
		}
	}
}

// Decode decodes the first event from b and returns the bytes consumed (>=1
// when b is non-empty). Exported for unit testing.
func Decode(b []byte) (Event, int) {
	if len(b) == 0 {
		return Event{}, 0
	}
	switch b[0] {
	case 0x03: // Ctrl-C
		return Event{Type: EventQuit}, 1
	case 0x04: // Ctrl-D (half page down)
		return key(KeyCtrlD), 1
	case 0x15: // Ctrl-U (half page up)
		return key(KeyCtrlU), 1
	case '\t':
		return key(KeyTab), 1
	case '\r', '\n':
		return key(KeyEnter), 1
	case 0x7f, 0x08:
		return key(KeyBackspace), 1
	case 0x1b: // escape sequences
		return decodeEscape(b)
	}
	if b[0] < 0x20 {
		// Unhandled control byte: ignore it but make progress.
		return key(KeyEsc), 1
	}
	r, size := utf8.DecodeRune(b)
	return Event{Type: EventKey, Key: KeyRune, Rune: r}, size
}

func decodeEscape(b []byte) (Event, int) {
	if len(b) < 2 {
		return key(KeyEsc), 1 // lone ESC
	}
	if b[1] != '[' && b[1] != 'O' {
		return key(KeyEsc), 1
	}
	if len(b) < 3 {
		return key(KeyEsc), 1
	}
	switch b[2] {
	case 'A':
		return key(KeyUp), 3
	case 'B':
		return key(KeyDown), 3
	case 'C':
		return key(KeyRight), 3
	case 'D':
		return key(KeyLeft), 3
	case 'Z':
		return key(KeyShiftTab), 3
	case 'H':
		return key(KeyHome), 3
	case 'F':
		return key(KeyEnd), 3
	}
	// Numeric sequences like ESC [ 5 ~
	if len(b) >= 4 && b[3] == '~' {
		switch b[2] {
		case '1', '7':
			return key(KeyHome), 4
		case '4', '8':
			return key(KeyEnd), 4
		case '5':
			return key(KeyPgUp), 4
		case '6':
			return key(KeyPgDn), 4
		case '3': // Delete -> treat as backspace
			return key(KeyBackspace), 4
		}
		return key(KeyEsc), 4
	}
	// Unknown CSI: consume what we have to avoid getting stuck.
	return key(KeyEsc), len(b)
}

func key(k Key) Event { return Event{Type: EventKey, Key: k} }
