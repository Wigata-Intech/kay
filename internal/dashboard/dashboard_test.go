package dashboard

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

type fakeClient struct {
	last string
	out  string
}

func (f *fakeClient) Run(cmd string) (string, error) { f.last = cmd; return f.out, nil }

func sampleSnap() metrics.Snapshot {
	return metrics.Snapshot{
		Hostname: "web-1", UptimeSec: 90000, NumCPU: 4, CPUPercent: 31,
		MemTotalKB: 8000000, MemAvailableKB: 800000, MemUsedPercent: 90,
		Load1: 0.4, Load5: 0.5, Load15: 0.6,
		Net:   []metrics.NetIface{{Name: "lo"}, {Name: "eth0", RxBytes: 500000, TxBytes: 250000}},
		Disks: []metrics.Disk{{Mount: "/", TotalBytes: 4000000000, UsedBytes: 1600000000}},
		Procs: []metrics.Proc{
			{PID: 1432, Name: "postgres", CPU: 22, Mem: 18},
			{PID: 980, Name: "nginx", CPU: 6, Mem: 1},
			{PID: 5, Name: "a very long process name that overflows", CPU: 1, Mem: 1},
		},
		Docker:        []metrics.Container{{ID: "abc123", Name: "web", Image: "nginx:latest", Status: "Up 2h"}},
		DockerPresent: true,
	}
}

func newModel() *model {
	m := &model{
		srv:      config.Server{Alias: "prod-1", Host: "10.0.0.1", Port: 22, User: "ubuntu"},
		client:   &fakeClient{},
		interval: 3 * time.Second,
	}
	m.snap = sampleSnap()
	m.have = true
	m.snapAt = time.Now()
	m.rebuildLists()
	return m
}

func TestRenderNeverExceedsViewport(t *testing.T) {
	tui.ColorEnabled = false
	m := newModel()
	for _, sz := range [][2]int{{80, 24}, {50, 15}, {200, 60}, {40, 10}} {
		for tab := 0; tab < len(tabNames); tab++ {
			m.tab = tab
			lines := m.render(sz[0], sz[1])
			if len(lines) > sz[1] {
				t.Errorf("tab %d %dx%d: %d lines exceed height", tab, sz[0], sz[1], len(lines))
			}
			for i, l := range lines {
				if w := tui.VisibleWidth(l); w > sz[0] {
					t.Errorf("tab %d %dx%d line %d width %d > %d: %q", tab, sz[0], sz[1], i, w, sz[0], l)
				}
			}
		}
	}
}

func TestTooSmall(t *testing.T) {
	m := newModel()
	if !strings.Contains(strings.Join(m.render(30, 8), "\n"), "too small") {
		t.Error("expected too-small message at 30x8")
	}
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
