package tui

import "testing"

func TestDecodeKeys(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		key  Key
		typ  EventType
		n    int
	}{
		{"up", []byte{0x1b, '[', 'A'}, KeyUp, EventKey, 3},
		{"down", []byte{0x1b, '[', 'B'}, KeyDown, EventKey, 3},
		{"right", []byte{0x1b, '[', 'C'}, KeyRight, EventKey, 3},
		{"left", []byte{0x1b, '[', 'D'}, KeyLeft, EventKey, 3},
		{"shifttab", []byte{0x1b, '[', 'Z'}, KeyShiftTab, EventKey, 3},
		{"pgup", []byte{0x1b, '[', '5', '~'}, KeyPgUp, EventKey, 4},
		{"pgdn", []byte{0x1b, '[', '6', '~'}, KeyPgDn, EventKey, 4},
		{"home", []byte{0x1b, '[', 'H'}, KeyHome, EventKey, 3},
		{"end", []byte{0x1b, '[', 'F'}, KeyEnd, EventKey, 3},
		{"ss3-up", []byte{0x1b, 'O', 'A'}, KeyUp, EventKey, 3},
		{"tab", []byte{'\t'}, KeyTab, EventKey, 1},
		{"enter-cr", []byte{'\r'}, KeyEnter, EventKey, 1},
		{"enter-lf", []byte{'\n'}, KeyEnter, EventKey, 1},
		{"backspace", []byte{0x7f}, KeyBackspace, EventKey, 1},
		{"esc", []byte{0x1b}, KeyEsc, EventKey, 1},
		{"ctrl-d", []byte{0x04}, KeyCtrlD, EventKey, 1},
		{"ctrl-u", []byte{0x15}, KeyCtrlU, EventKey, 1},
	}
	for _, c := range cases {
		ev, n := Decode(c.in)
		if ev.Type != c.typ || ev.Key != c.key || n != c.n {
			t.Errorf("%s: got type=%d key=%d n=%d, want type=%d key=%d n=%d",
				c.name, ev.Type, ev.Key, n, c.typ, c.key, c.n)
		}
	}
}

func TestDecodeRuneAndCtrlC(t *testing.T) {
	ev, n := Decode([]byte("q"))
	if ev.Type != EventKey || ev.Key != KeyRune || ev.Rune != 'q' || n != 1 {
		t.Errorf("rune q: %+v n=%d", ev, n)
	}
	ev, n = Decode([]byte{0x03})
	if ev.Type != EventQuit || n != 1 {
		t.Errorf("ctrl-c: %+v n=%d", ev, n)
	}
	// Multibyte rune consumes its full width.
	ev, n = Decode([]byte("é"))
	if ev.Key != KeyRune || n != 2 {
		t.Errorf("é: %+v n=%d", ev, n)
	}
}

func TestDecodeQueuedSequence(t *testing.T) {
	// Two events back-to-back in one buffer: Down then 'k'.
	b := append([]byte{0x1b, '[', 'B'}, 'k')
	ev, n := Decode(b)
	if ev.Key != KeyDown || n != 3 {
		t.Fatalf("first: %+v n=%d", ev, n)
	}
	ev, n = Decode(b[n:])
	if ev.Rune != 'k' || n != 1 {
		t.Fatalf("second: %+v n=%d", ev, n)
	}
}
