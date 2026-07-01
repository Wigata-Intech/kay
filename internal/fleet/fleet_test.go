// White-box: exercises unexported helpers (rows, render, statCell, colorFor,
// humanDurShort, firstLine) and the unexported hostState type directly.
package fleet

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// noColor disables tui coloring for the duration of a test so string
// assertions can match plain text, restoring the prior value afterwards.
func noColor(t *testing.T) {
	t.Helper()
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = old })
}

// funcName resolves a color function to its fully-qualified name so colorFor's
// choice can be asserted without invoking it.
func funcName(f func(string) string) string {
	return runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
}

// sampleHosts / sampleStates build a small fleet fixture: one online host, one
// errored host, and one not-yet-connected host.
func sampleHosts() []Host {
	return []Host{
		{Server: config.Server{Alias: "web", Host: "10.0.0.1"}},
		{Server: config.Server{Alias: "db", Host: "10.0.0.2"}},
		{Server: config.Server{Alias: "cache", Host: "10.0.0.3"}},
	}
}

func sampleStates() []hostState {
	snap := metrics.Snapshot{
		CPUPercent:     42,
		MemUsedPercent: 71,
		Load1:          1.25,
		UptimeSec:      3 * 24 * 3600, // 3 days
		Disks: []metrics.Disk{
			{Mount: "/", TotalBytes: 100, UsedBytes: 95},
		},
	}
	return []hostState{
		{snap: snap, ok: true},
		{err: errors.New("dial tcp: connection refused\nsecond line")},
		{ok: false},
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "single line", in: "boom", want: "boom"},
		{name: "empty", in: "", want: ""},
		{name: "multi line takes first", in: "first\nsecond\nthird", want: "first"},
		{name: "leading newline", in: "\nrest", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstLine(tt.in); got != tt.want {
				t.Errorf("firstLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHumanDurShort(t *testing.T) {
	tests := []struct {
		name string
		sec  float64
		want string
	}{
		{name: "days rounds down", sec: 3 * 24 * 3600, want: "3d"},
		{name: "just over a day", sec: 25 * 3600, want: "1d"},
		{name: "hours only", sec: 5 * 3600, want: "5h"},
		{name: "zero", sec: 0, want: "0h"},
		{name: "under an hour", sec: 600, want: "0h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanDurShort(tt.sec); got != tt.want {
				t.Errorf("humanDurShort(%v) = %q, want %q", tt.sec, got, tt.want)
			}
		})
	}
}

func TestColorFor(t *testing.T) {
	tests := []struct {
		name string
		pct  float64
		want string // name of the tui color function expected
	}{
		{name: "low is green", pct: 10, want: funcName(tui.Green)},
		{name: "just below yellow", pct: 69.9, want: funcName(tui.Green)},
		{name: "yellow boundary", pct: 70, want: funcName(tui.Yellow)},
		{name: "mid yellow", pct: 80, want: funcName(tui.Yellow)},
		{name: "red boundary", pct: 90, want: funcName(tui.Red)},
		{name: "high is red", pct: 99, want: funcName(tui.Red)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := funcName(colorFor(tt.pct)); got != tt.want {
				t.Errorf("colorFor(%v) = %s, want %s", tt.pct, got, tt.want)
			}
		})
	}
}

func TestStatCell(t *testing.T) {
	noColor(t)
	tests := []struct {
		name  string
		label string
		pct   float64
		want  string
	}{
		{name: "cpu formatted", label: "cpu", pct: 42, want: "cpu  42%"},
		{name: "mem rounds", label: "mem", pct: 71.4, want: "mem  71%"},
		{name: "full width", label: "dsk", pct: 100, want: "dsk 100%"},
		{name: "zero", label: "cpu", pct: 0, want: "cpu   0%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statCell(tt.label, tt.pct); got != tt.want {
				t.Errorf("statCell(%q, %v) = %q, want %q", tt.label, tt.pct, got, tt.want)
			}
		})
	}
}

func TestRows(t *testing.T) {
	noColor(t)
	hosts := sampleHosts()
	states := sampleStates()

	t.Run("plain online/error/connecting", func(t *testing.T) {
		got := rows(hosts, states, false)
		if len(got) != len(hosts) {
			t.Fatalf("rows len = %d, want %d", len(got), len(hosts))
		}
		// Online host: alias, host, stat cells, load, uptime.
		if !strings.Contains(got[0], "web") || !strings.Contains(got[0], "10.0.0.1") {
			t.Errorf("online row missing alias/host: %q", got[0])
		}
		for _, want := range []string{"cpu  42%", "mem  71%", "dsk  95%", "1.25", "3d"} {
			if !strings.Contains(got[0], want) {
				t.Errorf("online row missing %q: %q", want, got[0])
			}
		}
		// Errored host: offline + first line of error only.
		if !strings.Contains(got[1], "offline: dial tcp: connection refused") {
			t.Errorf("error row unexpected: %q", got[1])
		}
		if strings.Contains(got[1], "second line") {
			t.Errorf("error row leaked second line: %q", got[1])
		}
		// Not-yet-connected host.
		if !strings.Contains(got[2], "connecting") {
			t.Errorf("connecting row unexpected: %q", got[2])
		}
	})

	t.Run("anonymized masks alias and host", func(t *testing.T) {
		got := rows(hosts, states, true)
		if !strings.Contains(got[0], "server-1") || !strings.Contains(got[0], "demo.host") {
			t.Errorf("anon row not masked: %q", got[0])
		}
		if strings.Contains(got[0], "web") || strings.Contains(got[0], "10.0.0.1") {
			t.Errorf("anon row leaked real alias/host: %q", got[0])
		}
	})
}

func TestHandleFleetKey(t *testing.T) {
	tests := []struct {
		name         string
		ev           tui.Event
		startSel     int
		startRows    int
		startIval    time.Duration
		wantQuit     bool
		wantSel      int
		wantInterval time.Duration
		wantTrigger  bool
	}{
		{name: "q quits", ev: tui.Event{Rune: 'q'}, startIval: 5 * time.Second, wantQuit: true, wantInterval: 5 * time.Second},
		{name: "quit event quits", ev: tui.Event{Type: tui.EventQuit}, startIval: 5 * time.Second, wantQuit: true, wantInterval: 5 * time.Second},
		{name: "down moves selection", ev: tui.Event{Type: tui.EventKey, Key: tui.KeyDown}, startSel: 0, startRows: 3, startIval: 5 * time.Second, wantSel: 1, wantInterval: 5 * time.Second},
		{name: "j moves selection", ev: tui.Event{Rune: 'j'}, startSel: 0, startRows: 3, startIval: 5 * time.Second, wantSel: 1, wantInterval: 5 * time.Second},
		{name: "up moves selection", ev: tui.Event{Type: tui.EventKey, Key: tui.KeyUp}, startSel: 2, startRows: 3, startIval: 5 * time.Second, wantSel: 1, wantInterval: 5 * time.Second},
		{name: "G jumps to bottom", ev: tui.Event{Rune: 'G'}, startSel: 0, startRows: 3, startIval: 5 * time.Second, wantSel: 2, wantInterval: 5 * time.Second},
		{name: "g jumps to top", ev: tui.Event{Rune: 'g'}, startSel: 2, startRows: 3, startIval: 5 * time.Second, wantSel: 0, wantInterval: 5 * time.Second},
		{name: "r triggers refresh", ev: tui.Event{Rune: 'r'}, startIval: 5 * time.Second, wantInterval: 5 * time.Second, wantTrigger: true},
		{name: "plus grows interval", ev: tui.Event{Rune: '+'}, startIval: 5 * time.Second, wantInterval: 6 * time.Second},
		{name: "minus shrinks interval", ev: tui.Event{Rune: '-'}, startIval: 5 * time.Second, wantInterval: 4 * time.Second},
		{name: "minus clamped at one second", ev: tui.Event{Rune: '-'}, startIval: time.Second, wantInterval: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := tui.List{Selected: tt.startSel}
			if tt.startRows > 0 {
				rowsFixture := make([]string, tt.startRows)
				list.SetRows(rowsFixture)
				list.Selected = tt.startSel
			}
			interval := tt.startIval
			ticker := time.NewTicker(tt.startIval)
			defer ticker.Stop()
			triggered := false
			trigger := func() { triggered = true }

			quit := handleFleetKey(tt.ev, &list, &interval, ticker, trigger)
			if quit != tt.wantQuit {
				t.Errorf("quit = %v, want %v", quit, tt.wantQuit)
			}
			if tt.startRows > 0 && list.Selected != tt.wantSel {
				t.Errorf("selected = %d, want %d", list.Selected, tt.wantSel)
			}
			if interval != tt.wantInterval {
				t.Errorf("interval = %v, want %v", interval, tt.wantInterval)
			}
			if triggered != tt.wantTrigger {
				t.Errorf("triggered = %v, want %v", triggered, tt.wantTrigger)
			}
		})
	}
}

func TestRender(t *testing.T) {
	noColor(t)
	hosts := sampleHosts()
	states := sampleStates()

	t.Run("fits within bounds and shows cells", func(t *testing.T) {
		var list tui.List
		const w, h = 100, 20
		out := render(hosts, states, &list, 5*time.Second, w, h, false)
		if len(out) > h {
			t.Fatalf("render produced %d lines, exceeds height %d", len(out), h)
		}
		for i, line := range out {
			if got := len([]rune(line)); got > w {
				t.Errorf("line %d width %d exceeds %d: %q", i, got, w, line)
			}
		}
		joined := strings.Join(out, "\n")
		for _, want := range []string{"kay fleet", "Fleet — 1/3 online", "ALIAS", "web", "connecting"} {
			if !strings.Contains(joined, want) {
				t.Errorf("render output missing %q", want)
			}
		}
	})

	t.Run("too small returns a hint", func(t *testing.T) {
		var list tui.List
		out := render(hosts, states, &list, time.Second, 20, 4, false)
		joined := strings.Join(out, "\n")
		if !strings.Contains(joined, "terminal too small") {
			t.Errorf("expected too-small hint, got %q", joined)
		}
	})

	t.Run("wide terminal clamps content width", func(t *testing.T) {
		var list tui.List
		const w, h = 200, 24
		out := render(hosts, states, &list, time.Second, w, h, false)
		if len(out) > h {
			t.Fatalf("render produced %d lines, exceeds height %d", len(out), h)
		}
		// Content is capped at 120 columns even on a 200-wide terminal.
		for i, line := range out {
			if got := len([]rune(line)); got > 120 {
				t.Errorf("line %d width %d exceeds clamp 120: %q", i, got, line)
			}
		}
	})
}
