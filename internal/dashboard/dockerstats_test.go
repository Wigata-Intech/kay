// White-box: exercises the unexported docker-stats parser and dockStatsView
// overlay (sort toggle, loading gate), internal to the dashboard model.
package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// pumpStats drains one async docker-stats result and applies it.
func pumpStats(t *testing.T, m *model) {
	t.Helper()
	select {
	case sr := <-m.statResults:
		m.applyStats(sr)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stats result")
	}
}

func TestParsePercent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want float64
	}{
		{name: "percent", in: "12.34%", want: 12.34},
		{name: "spaces", in: " 5% ", want: 5},
		{name: "no sign", in: "7", want: 7},
		{name: "dash is zero", in: "--", want: 0},
		{name: "garbage is zero", in: "n/a", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePercent(tt.in); got != tt.want {
				t.Errorf("parsePercent(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseDockerStats(t *testing.T) {
	out := "web\t12.5%\t40.0%\t1.2GiB / 4GiB\t1MB / 2MB\n" +
		"db\t80.0%\t20.0%\t500MiB / 4GiB\t3MB / 1MB\n" +
		"short\tline\n" +
		"\n"
	got := parseDockerStats(out)
	if len(got) != 2 {
		t.Fatalf("parseDockerStats returned %d rows, want 2 (short/blank skipped)", len(got))
	}
	if got[0].name != "web" || got[0].cpu != 12.5 || got[0].mem != 40 {
		t.Errorf("row 0 = %+v, want web/12.5/40", got[0])
	}
	if got[1].memUse != "500MiB / 4GiB" || got[1].netIO != "3MB / 1MB" {
		t.Errorf("row 1 usage/io = %q / %q", got[1].memUse, got[1].netIO)
	}
}

func TestDockerStatsCommand(t *testing.T) {
	got := dockerStatsCommand()
	for _, want := range []string{"docker stats --no-stream", "{{.Name}}", "{{.CPUPerc}}", "{{.MemPerc}}"} {
		if !strings.Contains(got, want) {
			t.Errorf("dockerStatsCommand missing %q: %q", want, got)
		}
	}
}

func TestDockActionOpensStats(t *testing.T) {
	statsOut := "web\t12.5%\t40.0%\t1.2GiB / 4GiB\t1MB / 2MB\ndb\t80.0%\t20.0%\t500MiB / 4GiB\t3MB / 1MB"
	m := newModel()
	m.client = &fakeClient{out: statsOut}
	m.statResults = make(chan statResult, 1)
	m.tab = tabDocker

	m.dockAction(tui.Event{Rune: 't'})
	if m.dockStats == nil || !m.dockStats.loading {
		t.Fatal("'t' should open the stats overlay in a loading state")
	}
	pumpStats(t, m)
	if m.dockStats.loading {
		t.Error("loading should clear after the stats result")
	}
	// Default sort is by CPU descending: db (80) before web (12.5).
	if m.dockStats.stats[0].name != "db" {
		t.Errorf("top by CPU = %q, want db", m.dockStats.stats[0].name)
	}
}

func TestDockStatsSortToggle(t *testing.T) {
	statsOut := "web\t12.5%\t40.0%\t1.2GiB / 4GiB\t1MB / 2MB\ndb\t80.0%\t20.0%\t500MiB / 4GiB\t3MB / 1MB"
	m := newModel()
	m.client = &fakeClient{out: statsOut}
	m.statResults = make(chan statResult, 1)
	m.openDockStats()
	pumpStats(t, m)

	// Sort by memory: web (40) before db (20).
	m.handleDockStatsKey(tui.Event{Rune: 'm'})
	if m.dockStats.stats[0].name != "web" {
		t.Errorf("top by MEM = %q, want web", m.dockStats.stats[0].name)
	}
	// Back to CPU: db first.
	m.handleDockStatsKey(tui.Event{Rune: 'c'})
	if m.dockStats.stats[0].name != "db" {
		t.Errorf("top by CPU = %q, want db", m.dockStats.stats[0].name)
	}
}

func TestDockStatsLoadingIgnoresKeys(t *testing.T) {
	m := newModel()
	m.client = &fakeClient{out: "web\t1%\t1%\ta\tb"}
	m.statResults = make(chan statResult, 1)
	m.openDockStats() // loading, query in flight

	// Sort keys are ignored while loading.
	m.handleDockStatsKey(tui.Event{Rune: 'm'})
	if m.dockStats == nil || !m.dockStats.loading || m.dockStats.sortByMem {
		t.Fatal("keys other than close must be ignored while loading")
	}
	// Esc force-closes mid-scan.
	m.handleDockStatsKey(tui.Event{Key: tui.KeyEsc})
	if m.dockStats != nil {
		t.Error("Esc should close the overlay during loading")
	}
	// A stale result must not reopen it.
	pumpStats(t, m)
	if m.dockStats != nil {
		t.Error("a stale stats result must not reopen the overlay")
	}
}
