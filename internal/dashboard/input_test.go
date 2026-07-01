// White-box: tests for input.go — key handling and tab/detail actions.
package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestTrimLastRune(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"ascii", "abc", "ab"},
		{"empty", "", ""},
		{"multibyte", "héllo", "héll"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimLastRune(tt.in); got != tt.want {
				t.Errorf("trimLastRune(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestActiveList(t *testing.T) {
	m := newModel()
	tests := []struct {
		name string
		tab  int
		want *tui.List
	}{
		{"procs", tabProcesses, &m.proc},
		{"docker", tabDocker, &m.dock},
		{"network", tabNetwork, &m.net},
		{"disk", tabDisk, &m.disk},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.tab = tt.tab
			if got := m.activeList(); got != tt.want {
				t.Errorf("activeList(tab %d) = %p, want %p", tt.tab, got, tt.want)
			}
		})
	}
	t.Run("overview-nil", func(t *testing.T) {
		m.tab = tabOverview
		if m.activeList() != nil {
			t.Error("activeList on Overview should be nil")
		}
	})
}

func TestRunAction(t *testing.T) {
	noColor(t)
	t.Run("success", func(t *testing.T) {
		m := newModel()
		m.client = &fakeClient{out: ""}
		if got := m.runAction("kill 1", "sent SIGTERM"); !strings.Contains(got, "sent SIGTERM") {
			t.Errorf("runAction success = %q", got)
		}
	})
	t.Run("error-uses-output", func(t *testing.T) {
		m := newModel()
		m.client = &errClient{out: "no such process\ntrailing"}
		got := m.runAction("kill 999", "sent SIGTERM")
		if !strings.Contains(got, "no such process") || strings.Contains(got, "trailing") {
			t.Errorf("runAction error = %q, want first line of output", got)
		}
	})
	t.Run("error-empty-output-uses-err", func(t *testing.T) {
		m := newModel()
		m.client = &errClient{out: "   "}
		if got := m.runAction("kill 999", "ok"); !strings.Contains(got, "boom") {
			t.Errorf("runAction empty-output = %q, want err message", got)
		}
	})
}

func TestJumpHit(t *testing.T) {
	m := newModel()
	m.openDetail("logs", strings.Repeat("x\n", 10))
	t.Run("no-hits-noop", func(t *testing.T) {
		m.searchHits = nil
		m.searchIdx = 0
		m.jumpHit(1)
		if m.searchIdx != 0 {
			t.Errorf("jumpHit with no hits moved idx to %d", m.searchIdx)
		}
	})
	t.Run("cycles-forward-and-wraps", func(t *testing.T) {
		m.searchHits = []int{1, 5, 9}
		m.searchIdx = 0
		m.jumpHit(1)
		if m.searchIdx != 1 {
			t.Errorf("jumpHit forward idx = %d, want 1", m.searchIdx)
		}
		m.searchIdx = 2
		m.jumpHit(1)
		if m.searchIdx != 0 {
			t.Errorf("jumpHit wrap idx = %d, want 0", m.searchIdx)
		}
	})
	t.Run("cycles-backward-and-wraps", func(t *testing.T) {
		m.searchHits = []int{1, 5, 9}
		m.searchIdx = 0
		m.jumpHit(-1)
		if m.searchIdx != 2 {
			t.Errorf("jumpHit backward wrap idx = %d, want 2", m.searchIdx)
		}
	})
}

func TestProcAction(t *testing.T) {
	noColor(t)
	t.Run("s-cycles-sort", func(t *testing.T) {
		m := newModel()
		m.tab = tabProcesses
		m.proc.Selected = 0
		before := m.sortMode
		m.procAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 's'})
		if m.sortMode != (before+1)%4 {
			t.Errorf("sortMode = %d, want %d", m.sortMode, (before+1)%4)
		}
		if !strings.Contains(m.status, "sorted by") {
			t.Errorf("status = %q, want sort note", m.status)
		}
	})
	t.Run("enter-opens-detail", func(t *testing.T) {
		m := newModel()
		fc := &fakeClient{out: "Name: postgres"}
		m.client = fc
		m.tab = tabProcesses
		m.proc.Selected = 0
		m.procAction(tui.Event{Type: tui.EventKey, Key: tui.KeyEnter})
		if m.detail == nil {
			t.Fatal("Enter should open detail")
		}
		if !strings.Contains(fc.last, "/proc/1432/status") {
			t.Errorf("detail cmd = %q", fc.last)
		}
	})
	t.Run("x-opens-confirm", func(t *testing.T) {
		m := newModel()
		m.tab = tabProcesses
		m.proc.Selected = 0
		m.procAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'x'})
		if m.confirm == nil || !strings.Contains(m.confirm.text, "terminate") {
			t.Errorf("x should set terminate confirm, got %+v", m.confirm)
		}
	})
	t.Run("X-force-kill-confirm", func(t *testing.T) {
		m := newModel()
		m.tab = tabProcesses
		m.proc.Selected = 0
		m.procAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'X'})
		if m.confirm == nil || !strings.Contains(m.confirm.text, "FORCE") {
			t.Errorf("X should set FORCE confirm, got %+v", m.confirm)
		}
	})
	t.Run("read-only-blocks-kill", func(t *testing.T) {
		m := newModel()
		m.readOnly = true
		m.tab = tabProcesses
		m.proc.Selected = 0
		m.procAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'x'})
		if m.confirm != nil {
			t.Error("read-only should block confirm")
		}
		if !strings.Contains(m.status, "read-only") {
			t.Errorf("status = %q, want read-only note", m.status)
		}
	})
	t.Run("out-of-range-noop", func(t *testing.T) {
		m := newModel()
		m.tab = tabProcesses
		m.proc.Selected = 999
		m.procAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'x'})
		if m.confirm != nil {
			t.Error("out-of-range selection should not open a confirm")
		}
	})
}

func TestDockAction(t *testing.T) {
	noColor(t)
	t.Run("logs-opens-detail", func(t *testing.T) {
		m := newModel()
		fc := &fakeClient{out: "log output"}
		m.client = fc
		m.tab = tabDocker
		m.dock.Selected = 0
		m.dockAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'l'})
		if m.detail == nil {
			t.Fatal("'l' should open logs detail")
		}
		if !strings.Contains(fc.last, "docker logs") || !strings.Contains(fc.last, "abc123") {
			t.Errorf("logs cmd = %q", fc.last)
		}
	})
	t.Run("enter-inspects", func(t *testing.T) {
		m := newModel()
		fc := &fakeClient{out: "{}"}
		m.client = fc
		m.tab = tabDocker
		m.dock.Selected = 0
		m.dockAction(tui.Event{Type: tui.EventKey, Key: tui.KeyEnter})
		if !strings.Contains(fc.last, "docker inspect") {
			t.Errorf("inspect cmd = %q", fc.last)
		}
	})
	t.Run("R-restart-confirm", func(t *testing.T) {
		m := newModel()
		m.tab = tabDocker
		m.dock.Selected = 0
		m.dockAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'R'})
		if m.confirm == nil || !strings.Contains(m.confirm.text, "restart") {
			t.Errorf("R should set restart confirm, got %+v", m.confirm)
		}
	})
	t.Run("x-stop-confirm", func(t *testing.T) {
		m := newModel()
		m.tab = tabDocker
		m.dock.Selected = 0
		m.dockAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'x'})
		if m.confirm == nil || !strings.Contains(m.confirm.text, "stop") {
			t.Errorf("x should set stop confirm, got %+v", m.confirm)
		}
	})
	t.Run("read-only-blocks-restart", func(t *testing.T) {
		m := newModel()
		m.readOnly = true
		m.tab = tabDocker
		m.dock.Selected = 0
		m.dockAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'R'})
		if m.confirm != nil {
			t.Error("read-only should block restart confirm")
		}
	})
	t.Run("invalid-id-noop", func(t *testing.T) {
		m := newModel()
		m.snap.Docker = []metrics.Container{{ID: "bad id", Name: "x", Status: "Up"}}
		m.rebuildLists()
		m.tab = tabDocker
		m.dock.Selected = 0
		fc := &fakeClient{}
		m.client = fc
		m.dockAction(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'l'})
		if fc.last != "" {
			t.Errorf("invalid container id should run nothing, got %q", fc.last)
		}
	})
	t.Run("out-of-range-noop", func(t *testing.T) {
		m := newModel()
		m.tab = tabDocker
		m.dock.Selected = 99
		fc := &fakeClient{}
		m.client = fc
		m.dockAction(tui.Event{Type: tui.EventKey, Key: tui.KeyEnter})
		if fc.last != "" {
			t.Errorf("out-of-range dock selection ran %q", fc.last)
		}
	})
}

func TestHandleGlobalKey(t *testing.T) {
	tests := []struct {
		name        string
		ev          tui.Event
		wantHandled bool
		check       func(t *testing.T, m *model, r keyResult)
	}{
		{"q-quits", tui.Event{Key: tui.KeyRune, Rune: 'q'}, true, func(t *testing.T, m *model, r keyResult) {
			if !r.quit {
				t.Error("q should quit")
			}
		}},
		{"tab-advances", tui.Event{Key: tui.KeyTab}, true, func(t *testing.T, m *model, r keyResult) {
			if m.tab != tabProcesses {
				t.Errorf("tab = %d, want 1", m.tab)
			}
		}},
		{"bracket-prev-wraps", tui.Event{Key: tui.KeyRune, Rune: '['}, true, func(t *testing.T, m *model, r keyResult) {
			if m.tab != tabDisk {
				t.Errorf("[ from Overview should wrap to Disk (%d), got %d", tabDisk, m.tab)
			}
		}},
		{"bracket-next", tui.Event{Key: tui.KeyRune, Rune: ']'}, true, func(t *testing.T, m *model, r keyResult) {
			if m.tab != tabProcesses {
				t.Errorf("] should advance, got %d", m.tab)
			}
		}},
		{"digit-jumps", tui.Event{Key: tui.KeyRune, Rune: '4'}, true, func(t *testing.T, m *model, r keyResult) {
			if m.tab != tabNetwork {
				t.Errorf("'4' should jump to Network, got %d", m.tab)
			}
		}},
		{"r-refreshes", tui.Event{Key: tui.KeyRune, Rune: 'r'}, true, func(t *testing.T, m *model, r keyResult) {
			if !r.refreshNow {
				t.Error("r should refresh")
			}
		}},
		{"plus-interval", tui.Event{Key: tui.KeyRune, Rune: '+'}, true, func(t *testing.T, m *model, r keyResult) {
			if !r.intervalChanged || m.interval != 4*time.Second {
				t.Errorf("+ interval = %v changed=%v", m.interval, r.intervalChanged)
			}
		}},
		{"minus-interval", tui.Event{Key: tui.KeyRune, Rune: '-'}, true, func(t *testing.T, m *model, r keyResult) {
			if !r.intervalChanged || m.interval != 2*time.Second {
				t.Errorf("- interval = %v changed=%v", m.interval, r.intervalChanged)
			}
		}},
		{"unknown-not-handled", tui.Event{Key: tui.KeyRune, Rune: 'z'}, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel()
			r, handled := m.handleGlobalKey(tt.ev)
			if handled != tt.wantHandled {
				t.Errorf("handled = %v, want %v", handled, tt.wantHandled)
			}
			if tt.check != nil {
				tt.check(t, m, r)
			}
		})
	}

	t.Run("plus-caps-at-60s", func(t *testing.T) {
		m := newModel()
		m.interval = 60 * time.Second
		m.handleGlobalKey(tui.Event{Key: tui.KeyRune, Rune: '+'})
		if m.interval != 60*time.Second {
			t.Errorf("+ past cap = %v, want 60s", m.interval)
		}
	})
	t.Run("minus-floors-at-1s", func(t *testing.T) {
		m := newModel()
		m.interval = time.Second
		m.handleGlobalKey(tui.Event{Key: tui.KeyRune, Rune: '-'})
		if m.interval != time.Second {
			t.Errorf("- past floor = %v, want 1s", m.interval)
		}
	})
}

func TestHandleListNav(t *testing.T) {
	m := newModel()
	l := &m.proc
	l.Selected = 1
	handleListNav(l, tui.Event{Key: tui.KeyDown})
	if l.Selected != 2 {
		t.Errorf("Down = %d, want 2", l.Selected)
	}
	handleListNav(l, tui.Event{Key: tui.KeyHome})
	if l.Selected != 0 {
		t.Errorf("Home = %d, want 0", l.Selected)
	}
	handleListNav(l, tui.Event{Key: tui.KeyEnd})
	if l.Selected != len(m.snap.Procs)-1 {
		t.Errorf("End = %d, want %d", l.Selected, len(m.snap.Procs)-1)
	}
}

func TestHandleDetailNav(t *testing.T) {
	m := newModel()
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	m.openDetail("logs", strings.Join(lines, "\n"))

	t.Run("horizontal-pan", func(t *testing.T) {
		m.detailHoff = 0
		m.handleDetailNav(tui.Event{Key: tui.KeyRight})
		if m.detailHoff != 8 {
			t.Errorf("Right hoff = %d, want 8", m.detailHoff)
		}
		m.handleDetailNav(tui.Event{Key: tui.KeyLeft})
		if m.detailHoff != 0 {
			t.Errorf("Left hoff = %d, want 0", m.detailHoff)
		}
		// left clamps at 0
		m.handleDetailNav(tui.Event{Key: tui.KeyLeft})
		if m.detailHoff != 0 {
			t.Errorf("Left past 0 hoff = %d, want 0", m.detailHoff)
		}
	})
	t.Run("slash-starts-search", func(t *testing.T) {
		m.handleDetailNav(tui.Event{Key: tui.KeyRune, Rune: '/'})
		if !m.searching {
			t.Error("/ should start search")
		}
		m.searching = false
	})
	t.Run("scroll-and-jumps", func(t *testing.T) {
		m := newModel()
		lines := make([]string, 40)
		for i := range lines {
			lines[i] = "line"
		}
		m.openDetail("logs", strings.Join(lines, "\n"))
		m.handleDetailNav(tui.Event{Key: tui.KeyDown})
		m.handleDetailNav(tui.Event{Key: tui.KeyPgDn})
		m.handleDetailNav(tui.Event{Key: tui.KeyRune, Rune: 'G'})
		if m.detail.Offset() == 0 {
			t.Error("G should scroll to bottom")
		}
		m.handleDetailNav(tui.Event{Key: tui.KeyRune, Rune: 'g'})
		if m.detail.Offset() != 0 {
			t.Errorf("g should scroll to top, offset = %d", m.detail.Offset())
		}
		// n/N with hits set jumps around without panicking
		m.searchHits = []int{5, 20}
		m.handleDetailNav(tui.Event{Key: tui.KeyRune, Rune: 'n'})
		m.handleDetailNav(tui.Event{Key: tui.KeyRune, Rune: 'N'})
	})
	t.Run("esc-closes", func(t *testing.T) {
		m := newModel()
		m.openDetail("logs", "a\nb")
		m.handleDetailNav(tui.Event{Key: tui.KeyEsc})
		if m.detail != nil {
			t.Error("Esc should close detail")
		}
	})
	t.Run("q-closes", func(t *testing.T) {
		m.handleDetailNav(tui.Event{Key: tui.KeyRune, Rune: 'q'})
		if m.detail != nil {
			t.Error("q should close detail")
		}
	})
}

func TestKillConfirmFlow(t *testing.T) {
	tui.ColorEnabled = false
	fc := &fakeClient{}
	m := newModel()
	m.client = fc
	m.tab = tabProcesses
	m.proc.Selected = 0 // postgres, pid 1432

	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'x'})
	if m.confirm == nil {
		t.Fatal("expected a confirm prompt after 'x'")
	}
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'y'})
	if fc.last != "kill 1432" {
		t.Errorf("kill command = %q, want %q", fc.last, "kill 1432")
	}
	if m.confirm != nil {
		t.Error("confirm should be cleared after answering")
	}
}

func TestCancelConfirm(t *testing.T) {
	fc := &fakeClient{}
	m := newModel()
	m.client = fc
	m.tab = tabProcesses
	m.proc.Selected = 0
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'X'})
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'c'}) // any non-y cancels
	if m.confirm != nil {
		t.Error("confirm should be cancelled by a non-y key")
	}
	if fc.last != "" {
		t.Errorf("no command should run on cancel, got %q", fc.last)
	}
}

func TestTabNavigation(t *testing.T) {
	m := newModel()
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyTab})
	if m.tab != tabProcesses {
		t.Errorf("after Tab, tab = %d", m.tab)
	}
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: '3'})
	if m.tab != tabDocker {
		t.Errorf("after '3', tab = %d", m.tab)
	}
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyShiftTab})
	if m.tab != tabProcesses {
		t.Errorf("after Shift-Tab, tab = %d", m.tab)
	}
}

func TestProcSelectionMoves(t *testing.T) {
	m := newModel()
	m.tab = tabProcesses
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyDown})
	if m.proc.Selected != 1 {
		t.Errorf("selection after Down = %d, want 1", m.proc.Selected)
	}
	// vim 'j' moves down, 'k' moves up
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'j'})
	if m.proc.Selected != 2 {
		t.Errorf("selection after 'j' = %d, want 2", m.proc.Selected)
	}
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'k'})
	if m.proc.Selected != 1 {
		t.Errorf("selection after 'k' = %d, want 1", m.proc.Selected)
	}
}

func TestDetailSearch(t *testing.T) {
	tui.ColorEnabled = false
	m := newModel()
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	lines[37] = "ERROR something bad happened"
	m.openDetail("logs web", strings.Join(lines, "\n"))

	// type "/error" then Enter
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: '/'})
	for _, r := range "error" {
		m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: r})
	}
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyEnter})

	if len(m.searchHits) != 1 || m.searchHits[0] != 37 {
		t.Errorf("search hits = %v, want [37]", m.searchHits)
	}
	if m.searching {
		t.Error("search input should close on Enter")
	}
	// Esc/q closes the overlay
	m.handleKey(tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'q'})
	if m.detail != nil {
		t.Error("overlay should close on q")
	}
}
