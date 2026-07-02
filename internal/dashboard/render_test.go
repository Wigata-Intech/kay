// White-box: tests for render.go — the view builders and overlays.
package dashboard

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

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
	joined := strings.Join(m.overviewSystem(m.snap), "\n")
	for _, want := range []string{"CPU", "LOAD", "cpu"} {
		if !strings.Contains(joined, want) {
			t.Errorf("overviewSystem missing %q in:\n%s", want, joined)
		}
	}
}

func TestHelpOverlay(t *testing.T) {
	noColor(t)
	m := newModel()
	m.handleKey(tui.Event{Rune: '?'})
	if !m.help {
		t.Fatal("? should open the help overlay")
	}
	m.handleKey(tui.Event{Rune: 'j'})
	if m.help {
		t.Error("any key should close the help overlay")
	}
	m.help = true
	if out := strings.Join(m.render(100, 40), "\n"); !strings.Contains(out, "Keybindings") {
		t.Errorf("help overlay not rendered:\n%s", out)
	}
}

func TestOverviewMemoryAndDisk(t *testing.T) {
	noColor(t)
	m := newModel()
	if got := strings.Join(m.overviewMemory(m.snap), "\n"); !strings.Contains(got, "MEM") || !strings.Contains(got, "mem") {
		t.Errorf("overviewMemory missing MEM/mem trend:\n%s", got)
	}
	if got := strings.Join(m.overviewDisk(m.snap), "\n"); !strings.Contains(got, "DISK") {
		t.Errorf("overviewDisk missing DISK:\n%s", got)
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
