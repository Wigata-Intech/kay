package dashboard

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// dockStatsView is the on-demand "top containers" overlay: a live `docker stats`
// snapshot, sortable by CPU or memory. The scan is slow, so it runs
// asynchronously with a loading gate like the disk explorer.
type dockStatsView struct {
	stats     []containerStat
	list      tui.List
	sortByMem bool // false = sort by CPU%
	loading   bool
}

// containerStat is one row of `docker stats --no-stream`.
type containerStat struct {
	name   string
	cpu    float64 // percent
	mem    float64 // percent
	memUse string  // e.g. "1.2GiB / 4GiB"
	netIO  string  // e.g. "1.1MB / 3.2MB"
}

// dockerStatsCommand returns a one-shot, tab-delimited docker stats query.
func dockerStatsCommand() string {
	return "docker stats --no-stream --format " +
		"'{{.Name}}\\t{{.CPUPerc}}\\t{{.MemPerc}}\\t{{.MemUsage}}\\t{{.NetIO}}' 2>/dev/null"
}

// parseDockerStats parses tab-delimited docker stats output. Percentages have
// their trailing '%' stripped; unparseable lines are skipped.
func parseDockerStats(out string) []containerStat {
	var cs []containerStat
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		cs = append(cs, containerStat{
			name:   f[0],
			cpu:    parsePercent(f[1]),
			mem:    parsePercent(f[2]),
			memUse: strings.TrimSpace(f[3]),
			netIO:  strings.TrimSpace(f[4]),
		})
	}
	return cs
}

// parsePercent parses "12.34%" (or "--") into a float; unparseable is 0.
func parsePercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// openDockStats opens the overlay and kicks off the first stats query.
func (m *model) openDockStats() {
	m.dockStats = &dockStatsView{}
	m.startStatsLoad()
}

// startStatsLoad runs docker stats in a goroutine (it blocks ~1-2s) and reports
// on m.statResults.
func (m *model) startStatsLoad() {
	m.dockStats.loading = true
	cl := m.client // snapshot: reconnect may replace m.client later
	ch := m.statResults
	go func() {
		out, err := cl.Run(dockerStatsCommand())
		ch <- statResult{out: out, err: err}
	}()
}

// applyStats installs a completed stats query, ignoring stale results (the
// overlay was closed).
func (m *model) applyStats(sr statResult) {
	if m.dockStats == nil || !m.dockStats.loading {
		return
	}
	m.dockStats.loading = false
	if sr.err != nil {
		m.status = tui.Red("docker stats failed: " + tui.FirstLine(sr.err.Error()))
	}
	m.dockStats.stats = parseDockerStats(sr.out)
	m.rebuildStatRows()
}

// rebuildStatRows sorts by the active key and formats the list rows.
func (m *model) rebuildStatRows() {
	ds := m.dockStats
	sort.SliceStable(ds.stats, func(i, j int) bool {
		if ds.sortByMem {
			return ds.stats[i].mem > ds.stats[j].mem
		}
		return ds.stats[i].cpu > ds.stats[j].cpu
	})
	rows := make([]string, len(ds.stats))
	for i, c := range ds.stats {
		rows[i] = fmt.Sprintf("%-20s %s %s  %-18s %s",
			tui.Truncate(c.name, 20),
			tui.ThreshColor(fmt.Sprintf("%5.1f%%", c.cpu), c.cpu),
			tui.ThreshColor(fmt.Sprintf("%5.1f%%", c.mem), c.mem),
			c.memUse, c.netIO)
	}
	ds.list.Header = fmt.Sprintf("%-20s %6s %6s  %-18s %s", "NAME", "CPU", "MEM", "MEM USAGE", "NET I/O")
	ds.list.SetRows(rows)
	ds.list.Selected = 0
}

// handleDockStatsKey drives the overlay: sort by CPU/MEM, reload, scroll, close.
func (m *model) handleDockStatsKey(ev tui.Event) {
	if m.dockStats.loading {
		if ev.Key == tui.KeyEsc || ev.Rune == 'q' {
			m.dockStats = nil
		}
		return
	}

	switch {
	case ev.Key == tui.KeyEsc, ev.Rune == 'q':
		m.dockStats = nil
	case ev.Rune == 'c':
		m.dockStats.sortByMem = false
		m.rebuildStatRows()
	case ev.Rune == 'm':
		m.dockStats.sortByMem = true
		m.rebuildStatRows()
	case ev.Rune == 'r':
		m.startStatsLoad()
	default:
		handleListNav(&m.dockStats.list, ev)
	}
}

// renderDockStats builds the overlay view for the given inner viewport.
func (m *model) renderDockStats(width, height int) (title string, body []string) {
	if m.dockStats.loading {
		return "Docker · loading stats…", []string{"", tui.Dim("  running docker stats … (esc to cancel)")}
	}
	sortKey := "CPU"
	if m.dockStats.sortByMem {
		sortKey = "MEM"
	}
	title = fmt.Sprintf("Docker stats · top by %s (%d)", sortKey, len(m.dockStats.stats))
	if len(m.dockStats.stats) == 0 {
		return title, []string{"", tui.Dim("  no running containers")}
	}
	return title, m.dockStats.list.Render(width, height)
}
