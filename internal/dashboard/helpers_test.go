// White-box (package dashboard): exercises unexported pure helpers directly.
package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// noColor disables ANSI colouring for the duration of a test so string
// assertions compare against plain text, restoring the previous state after.
func noColor(t *testing.T) {
	t.Helper()
	prev := tui.ColorEnabled
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = prev })
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want string
	}{
		{"bytes", 512, "512 B"},
		{"kib", 1024, "1.0 KB"},
		{"mib", 1024 * 1024, "1.0 MB"},
		{"gib", 1024 * 1024 * 1024, "1.0 GB"},
		{"zero", 0, "0 B"},
		{"exabyte", 1024 * 1024 * 1024 * 1024 * 1024 * 1024 * 2, "2.0 EB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanBytes(tt.in); got != tt.want {
				t.Errorf("humanBytes(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHumanKB(t *testing.T) {
	if got := humanKB(1024); got != "1.0 MB" {
		t.Errorf("humanKB(1024) = %q, want %q", got, "1.0 MB")
	}
	if got := humanKB(0); got != "0 B" {
		t.Errorf("humanKB(0) = %q, want %q", got, "0 B")
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		name string
		sec  float64
		want string
	}{
		{"hours-minutes", 3660, "1h 1m"},
		{"days", 90000, "1d 1h 0m"},
		{"zero", 0, "0h 0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanDuration(tt.sec); got != tt.want {
				t.Errorf("humanDuration(%v) = %q, want %q", tt.sec, got, tt.want)
			}
		})
	}
}

func TestMakeBar(t *testing.T) {
	noColor(t)
	tests := []struct {
		name  string
		pct   float64
		width int
		want  string
	}{
		{"half", 50, 10, "[█████·····]"},
		{"full", 100, 4, "[████]"},
		{"empty", 0, 4, "[····]"},
		{"negative-clamps-empty", -20, 4, "[····]"},
		{"over-100-clamps-full", 150, 4, "[████]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := makeBar(tt.pct, tt.width); got != tt.want {
				t.Errorf("makeBar(%v,%d) = %q, want %q", tt.pct, tt.width, got, tt.want)
			}
		})
	}
}

func TestGaugeLine(t *testing.T) {
	noColor(t)
	got := gaugeLine("CPU", 50, 10, "4 cores")
	if !strings.Contains(got, "CPU") || !strings.Contains(got, "50%") || !strings.Contains(got, "4 cores") {
		t.Errorf("gaugeLine missing parts: %q", got)
	}
	if !strings.Contains(got, "[█████·····]") {
		t.Errorf("gaugeLine missing bar: %q", got)
	}
}

func TestLoadColor(t *testing.T) {
	prev := tui.ColorEnabled
	tui.ColorEnabled = true
	t.Cleanup(func() { tui.ColorEnabled = prev })
	tests := []struct {
		name string
		load float64
		ncpu int
		want func(string) string
	}{
		{"green-idle", 1, 4, tui.Green},
		{"yellow-over-70pct", 3, 4, tui.Yellow},
		{"red-over-cores", 5, 4, tui.Red},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := loadColor(tt.load, tt.ncpu, "x"), tt.want("x"); got != want {
				t.Errorf("loadColor(%v,%d) = %q, want %q", tt.load, tt.ncpu, got, want)
			}
		})
	}
}

func TestColorStatus(t *testing.T) {
	prev := tui.ColorEnabled
	tui.ColorEnabled = true
	t.Cleanup(func() { tui.ColorEnabled = prev })
	tests := []struct {
		name   string
		status string
		want   func(string) string
	}{
		{"up-green", "Up 2 hours", tui.Green},
		{"healthy-green", "Up 2 hours (healthy)", tui.Green},
		{"unhealthy-red", "Up 2 hours (unhealthy)", tui.Red},
		{"exited-red", "Exited (0) 3 minutes ago", tui.Red},
		{"dead-red", "dead", tui.Red},
		{"restarting-red", "restarting (1)", tui.Red},
		{"unknown-plain", "Created", func(s string) string { return s }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := colorStatus(tt.status), tt.want(tt.status); got != want {
				t.Errorf("colorStatus(%q) = %q, want %q", tt.status, got, want)
			}
		})
	}
}

func TestValidID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"alnum", "abc123", true},
		{"with-allowed-punct", "web_1.2-3", true},
		{"empty", "", false},
		{"space", "web 1", false},
		{"slash", "a/b", false},
		{"semicolon", "id;rm", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validID(tt.in); got != tt.want {
				t.Errorf("validID(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestSortName(t *testing.T) {
	tests := []struct {
		name string
		mode int
		want string
	}{
		{"cpu", 0, "CPU"},
		{"mem", 1, "MEM"},
		{"pid", 2, "PID"},
		{"name", 3, "name"},
		{"default", 99, "CPU"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sortName(tt.mode); got != tt.want {
				t.Errorf("sortName(%d) = %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

func TestAppendHist(t *testing.T) {
	t.Run("appends", func(t *testing.T) {
		h := appendHist(nil, 1, 3)
		h = appendHist(h, 2, 3)
		if len(h) != 2 || h[0] != 1 || h[1] != 2 {
			t.Errorf("appendHist = %v", h)
		}
	})
	t.Run("caps-at-max", func(t *testing.T) {
		var h []float64
		for i := 0; i < 5; i++ {
			h = appendHist(h, float64(i), 3)
		}
		if len(h) != 3 || h[0] != 2 || h[2] != 4 {
			t.Errorf("appendHist capped = %v, want tail [2 3 4]", h)
		}
	})
}

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

func TestSparkline(t *testing.T) {
	noColor(t)
	t.Run("empty-collecting", func(t *testing.T) {
		if got := sparkline(nil, 8); !strings.Contains(got, "collecting") {
			t.Errorf("sparkline(nil) = %q, want collecting note", got)
		}
	})
	t.Run("renders-blocks", func(t *testing.T) {
		got := sparkline([]float64{0, 50, 100}, 8)
		if got != "▁▅█" {
			t.Errorf("sparkline = %q, want %q", got, "▁▅█")
		}
	})
	t.Run("clamps-and-truncates-to-width", func(t *testing.T) {
		got := sparkline([]float64{-10, 200, 200, 200}, 2)
		if []rune(got)[0] != '█' || len([]rune(got)) != 2 {
			t.Errorf("sparkline clamp/trunc = %q", got)
		}
	})
}

func TestNetRate(t *testing.T) {
	t.Run("no-prev-zero", func(t *testing.T) {
		m := newModel()
		m.prev = nil
		rx, tx := m.netRate(metrics.NetIface{Name: "eth0"})
		if rx != 0 || tx != 0 {
			t.Errorf("netRate without prev = (%v,%v), want (0,0)", rx, tx)
		}
	})
	t.Run("computes-rate", func(t *testing.T) {
		m := newModel()
		prev := metrics.Snapshot{Net: []metrics.NetIface{{Name: "eth0", RxBytes: 1000, TxBytes: 2000}}}
		m.prev = &prev
		m.prevAt = time.Now().Add(-2 * time.Second)
		m.snapAt = time.Now()
		rx, tx := m.netRate(metrics.NetIface{Name: "eth0", RxBytes: 3000, TxBytes: 6000})
		// ~2000 bytes / ~2s and ~4000 bytes / ~2s
		if rx < 900 || rx > 1100 || tx < 1900 || tx > 2100 {
			t.Errorf("netRate = (%v,%v), want ~ (1000,2000)", rx, tx)
		}
	})
	t.Run("counter-reset-clamps-zero", func(t *testing.T) {
		m := newModel()
		prev := metrics.Snapshot{Net: []metrics.NetIface{{Name: "eth0", RxBytes: 5000, TxBytes: 5000}}}
		m.prev = &prev
		m.prevAt = time.Now().Add(-time.Second)
		m.snapAt = time.Now()
		rx, tx := m.netRate(metrics.NetIface{Name: "eth0", RxBytes: 100, TxBytes: 100})
		if rx != 0 || tx != 0 {
			t.Errorf("netRate on counter reset = (%v,%v), want (0,0)", rx, tx)
		}
	})
	t.Run("unknown-iface-zero", func(t *testing.T) {
		m := newModel()
		prev := metrics.Snapshot{Net: []metrics.NetIface{{Name: "eth0"}}}
		m.prev = &prev
		m.prevAt = time.Now().Add(-time.Second)
		m.snapAt = time.Now()
		rx, tx := m.netRate(metrics.NetIface{Name: "wlan0", RxBytes: 100})
		if rx != 0 || tx != 0 {
			t.Errorf("netRate for unknown iface = (%v,%v), want (0,0)", rx, tx)
		}
	})
}

func TestOverviewDocker(t *testing.T) {
	noColor(t)
	tests := []struct {
		name string
		snap metrics.Snapshot
		want string
	}{
		{"not-installed", metrics.Snapshot{DockerPresent: false}, "not installed"},
		{"none-running", metrics.Snapshot{DockerPresent: true}, "no running containers"},
		{
			"running-with-health",
			metrics.Snapshot{DockerPresent: true, Docker: []metrics.Container{
				{Status: "Up (healthy)"},
				{Status: "Up (unhealthy)"},
			}},
			"2 running",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel()
			if got := m.overviewDocker(tt.snap); !strings.Contains(got, tt.want) {
				t.Errorf("overviewDocker = %q, want to contain %q", got, tt.want)
			}
		})
	}
	t.Run("health-counts", func(t *testing.T) {
		m := newModel()
		got := m.overviewDocker(metrics.Snapshot{DockerPresent: true, Docker: []metrics.Container{
			{Status: "Up (healthy)"},
			{Status: "Up (unhealthy)"},
		}})
		if !strings.Contains(got, "1 healthy") || !strings.Contains(got, "1 unhealthy") {
			t.Errorf("overviewDocker health = %q", got)
		}
	})
}

func TestOverviewProcs(t *testing.T) {
	noColor(t)
	m := newModel()
	out := m.overviewProcs(m.snap, 2)
	// header + 2 rows
	if len(out) != 3 {
		t.Fatalf("overviewProcs len = %d, want 3", len(out))
	}
	if !strings.Contains(out[1], "postgres") {
		t.Errorf("first proc row = %q, want postgres", out[1])
	}
	t.Run("anon-masks-names", func(t *testing.T) {
		am := newModel()
		am.anon = true
		out := am.overviewProcs(am.snap, 1)
		if strings.Contains(strings.Join(out, ""), "postgres") {
			t.Errorf("anon overviewProcs leaked real name: %v", out)
		}
	})
}

func TestOverviewSystem(t *testing.T) {
	noColor(t)
	m := newModel()
	out := m.overviewSystem(m.snap)
	joined := strings.Join(out, "\n")
	for _, want := range []string{"CPU", "MEM", "DISK", "LOAD", "cpu", "mem"} {
		if !strings.Contains(joined, want) {
			t.Errorf("overviewSystem missing %q in:\n%s", want, joined)
		}
	}
}

func TestKeyHints(t *testing.T) {
	noColor(t)
	tests := []struct {
		name     string
		tab      int
		readOnly bool
		wantHas  []string
		wantMiss []string
	}{
		{"overview", tabOverview, false, []string{"Tab", "quit"}, nil},
		{"procs-rw", tabProcesses, false, []string{"x term", "X kill", "sort"}, nil},
		{"procs-ro", tabProcesses, true, []string{"read-only", "sort", "details"}, []string{"X kill"}},
		{"docker-rw", tabDocker, false, []string{"logs", "R restart", "x stop"}, nil},
		{"docker-ro", tabDocker, true, []string{"logs", "read-only"}, []string{"R restart"}},
		{"network", tabNetwork, false, []string{"select"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel()
			m.tab = tt.tab
			m.readOnly = tt.readOnly
			got := m.keyHints()
			for _, w := range tt.wantHas {
				if !strings.Contains(got, w) {
					t.Errorf("keyHints missing %q: %q", w, got)
				}
			}
			for _, w := range tt.wantMiss {
				if strings.Contains(got, w) {
					t.Errorf("keyHints should not contain %q: %q", w, got)
				}
			}
		})
	}
}

func TestBlockedReadOnly(t *testing.T) {
	noColor(t)
	t.Run("read-only-blocks-and-sets-status", func(t *testing.T) {
		m := newModel()
		m.readOnly = true
		if !m.blockedReadOnly() {
			t.Fatal("blockedReadOnly should be true in read-only mode")
		}
		if !strings.Contains(m.status, "read-only") {
			t.Errorf("status = %q, want read-only note", m.status)
		}
	})
	t.Run("writable-allows", func(t *testing.T) {
		m := newModel()
		if m.blockedReadOnly() {
			t.Error("blockedReadOnly should be false when writable")
		}
	})
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

func TestFooter(t *testing.T) {
	noColor(t)
	t.Run("confirm-prompt", func(t *testing.T) {
		m := newModel()
		m.confirm = &confirmPrompt{text: "terminate?", run: func() string { return "" }}
		if got := m.footer(80); !strings.Contains(got, "terminate?") || !strings.Contains(got, "[y/N]") {
			t.Errorf("footer confirm = %q", got)
		}
	})
	t.Run("status", func(t *testing.T) {
		m := newModel()
		m.status = "did a thing"
		if got := m.footer(80); !strings.Contains(got, "did a thing") {
			t.Errorf("footer status = %q", got)
		}
	})
	t.Run("hints-default", func(t *testing.T) {
		m := newModel()
		if got := m.footer(80); !strings.Contains(got, "quit") {
			t.Errorf("footer hints = %q", got)
		}
	})
}

func TestDetailFooter(t *testing.T) {
	noColor(t)
	t.Run("searching", func(t *testing.T) {
		m := newModel()
		m.openDetail("logs", "a\nb\nc")
		m.searching = true
		m.searchQuery = "err"
		if got := m.detailFooter(80); !strings.Contains(got, "search: /err") {
			t.Errorf("detailFooter searching = %q", got)
		}
	})
	t.Run("position-and-hints", func(t *testing.T) {
		m := newModel()
		m.openDetail("logs", "a\nb\nc")
		got := m.detailFooter(80)
		if !strings.Contains(got, "ln 1/3") || !strings.Contains(got, "search") {
			t.Errorf("detailFooter = %q", got)
		}
	})
	t.Run("hoff-and-match-count", func(t *testing.T) {
		m := newModel()
		m.openDetail("logs", "a\nb\nc")
		m.detailHoff = 8
		m.searchHits = []int{0, 2}
		got := m.detailFooter(80)
		if !strings.Contains(got, "col+8") || !strings.Contains(got, "match 1/2") {
			t.Errorf("detailFooter hoff/match = %q", got)
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

// errClient returns a non-nil error from Run, for exercising the failure paths
// of runAction / actions that inspect errors.
type errClient struct{ out string }

func (e *errClient) Run(cmd string) (string, error) {
	return e.out, errBoom
}

var errBoom = boomError("boom")

type boomError string

func (b boomError) Error() string { return string(b) }

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

func TestHighlightMatches(t *testing.T) {
	prev := tui.ColorEnabled
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = prev })
	tests := []struct {
		name, line, query, want string
	}{
		{"empty-query-unchanged", "hello world", "", "hello world"},
		{"whitespace-query-unchanged", "hello", "   ", "hello"},
		{"no-match-unchanged", "hello", "zzz", "hello"},
		{"case-insensitive-match", "ERROR here", "error", "ERROR here"},
		{"multiple-matches", "aXaXa", "x", "aXaXa"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// With colour disabled Reverse() is a no-op, so highlighted text
			// equals the original — this exercises the match-scanning branches.
			if got := highlightMatches(tt.line, tt.query); got != tt.want {
				t.Errorf("highlightMatches(%q,%q) = %q, want %q", tt.line, tt.query, got, tt.want)
			}
		})
	}
}

func TestRenderDetailBody(t *testing.T) {
	noColor(t)
	m := newModel()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "content line"
	}
	m.openDetail("logs", strings.Join(lines, "\n"))
	t.Run("returns-visible-rows", func(t *testing.T) {
		out := m.renderDetailBody(40, 5)
		if len(out) != 5 {
			t.Errorf("renderDetailBody rows = %d, want 5", len(out))
		}
		for _, r := range out {
			if !strings.Contains(r, "content line") {
				t.Errorf("row missing content: %q", r)
			}
		}
	})
	t.Run("marks-current-search-hit", func(t *testing.T) {
		m.searchHits = []int{0}
		m.searchIdx = 0
		out := m.renderDetailBody(40, 3)
		if !strings.Contains(out[0], "▌") {
			t.Errorf("current hit row should carry a marker: %q", out[0])
		}
	})
}

func TestTooSmallHelper(t *testing.T) {
	out := tooSmall(30, 8)
	if !strings.Contains(strings.Join(out, "\n"), "30x8") {
		t.Errorf("tooSmall = %v, want dimensions", out)
	}
}
