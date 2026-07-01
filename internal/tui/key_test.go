package tui_test

import (
	"os"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// TestDecode covers single-event decoding. Positive cases (recognised escape
// sequences, control bytes, and runes) come first, then the error/fallback
// cases (lone ESC, unknown CSI) in the order Decode handles them.
func TestDecode(t *testing.T) {
	tests := []struct {
		name     string
		in       []byte
		wantType tui.EventType
		wantKey  tui.Key
		wantRune rune
		wantN    int
	}{
		// positive: arrow / navigation escape sequences
		{"up", []byte{0x1b, '[', 'A'}, tui.EventKey, tui.KeyUp, 0, 3},
		{"down", []byte{0x1b, '[', 'B'}, tui.EventKey, tui.KeyDown, 0, 3},
		{"right", []byte{0x1b, '[', 'C'}, tui.EventKey, tui.KeyRight, 0, 3},
		{"left", []byte{0x1b, '[', 'D'}, tui.EventKey, tui.KeyLeft, 0, 3},
		{"shifttab", []byte{0x1b, '[', 'Z'}, tui.EventKey, tui.KeyShiftTab, 0, 3},
		{"home", []byte{0x1b, '[', 'H'}, tui.EventKey, tui.KeyHome, 0, 3},
		{"end", []byte{0x1b, '[', 'F'}, tui.EventKey, tui.KeyEnd, 0, 3},
		{"ss3-up", []byte{0x1b, 'O', 'A'}, tui.EventKey, tui.KeyUp, 0, 3},
		{"pgup", []byte{0x1b, '[', '5', '~'}, tui.EventKey, tui.KeyPgUp, 0, 4},
		{"pgdn", []byte{0x1b, '[', '6', '~'}, tui.EventKey, tui.KeyPgDn, 0, 4},
		// positive: control bytes and runes
		{"tab", []byte{'\t'}, tui.EventKey, tui.KeyTab, 0, 1},
		{"enter-cr", []byte{'\r'}, tui.EventKey, tui.KeyEnter, 0, 1},
		{"enter-lf", []byte{'\n'}, tui.EventKey, tui.KeyEnter, 0, 1},
		{"backspace", []byte{0x7f}, tui.EventKey, tui.KeyBackspace, 0, 1},
		{"ctrl-d", []byte{0x04}, tui.EventKey, tui.KeyCtrlD, 0, 1},
		{"ctrl-u", []byte{0x15}, tui.EventKey, tui.KeyCtrlU, 0, 1},
		{"rune-ascii", []byte("q"), tui.EventKey, tui.KeyRune, 'q', 1},
		{"rune-multibyte", []byte("é"), tui.EventKey, tui.KeyRune, 'é', 2},
		{"ctrl-c-quit", []byte{0x03}, tui.EventQuit, tui.KeyRune, 0, 1},
		// error / fallback: lone ESC and unrecognised sequences still make progress
		{"lone-esc", []byte{0x1b}, tui.EventKey, tui.KeyEsc, 0, 1},
		{"esc-non-csi", []byte{0x1b, 'x'}, tui.EventKey, tui.KeyEsc, 0, 1},
		{"unknown-csi-tilde", []byte{0x1b, '[', '9', '~'}, tui.EventKey, tui.KeyEsc, 0, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, n := tui.Decode(tt.in)
			if ev.Type != tt.wantType || ev.Key != tt.wantKey || ev.Rune != tt.wantRune || n != tt.wantN {
				t.Errorf("Decode(%v) = {type:%d key:%d rune:%q} n=%d; want {type:%d key:%d rune:%q} n=%d",
					tt.in, ev.Type, ev.Key, ev.Rune, n, tt.wantType, tt.wantKey, tt.wantRune, tt.wantN)
			}
		})
	}
}

// TestDecodeQueued verifies two events packed into one buffer decode in
// sequence: the second Decode consumes the remainder. Kept as an ordered
// sequence (stateful across two calls) rather than a table.
func TestDecodeQueued(t *testing.T) {
	b := append([]byte{0x1b, '[', 'B'}, 'k') // Down, then 'k'
	ev, n := tui.Decode(b)
	if ev.Key != tui.KeyDown || n != 3 {
		t.Fatalf("first event: key=%d n=%d, want KeyDown n=3", ev.Key, n)
	}
	ev, n = tui.Decode(b[n:])
	if ev.Rune != 'k' || n != 1 {
		t.Fatalf("second event: rune=%q n=%d, want 'k' n=1", ev.Rune, n)
	}
}

// TestNewReaderReadEvent drives the Reader over an os.Pipe so ReadEvent decodes
// a real byte stream (the constructor and read loop otherwise need a terminal).
func TestNewReaderReadEvent(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = pr.Close() }()

	r := tui.NewReader(pr)
	if r == nil {
		t.Fatal("NewReader returned nil")
	}

	go func() {
		_, _ = pw.Write([]byte{'\t'}) // a Tab key
		_ = pw.Close()
	}()

	ev, err := r.ReadEvent()
	if err != nil {
		t.Fatalf("ReadEvent: %v", err)
	}
	if ev.Type != tui.EventKey || ev.Key != tui.KeyTab {
		t.Errorf("ReadEvent = {type:%d key:%d}, want Tab key", ev.Type, ev.Key)
	}
}
