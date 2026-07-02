package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// overviewNet returns up to max network interfaces for the Overview, busiest
// first (by current rate, then cumulative bytes), with a note when there are
// more. Avoids flooding the panel on hosts with many veth/bridge interfaces.
func (m *model) overviewNet(max int) []string {
	type ir struct {
		ni     metrics.NetIface
		rx, tx float64
	}
	var list []ir
	for _, ni := range m.snap.Net {
		if ni.Name == "lo" {
			continue
		}
		rx, tx := m.netRate(ni)
		list = append(list, ir{ni, rx, tx})
	}
	sort.Slice(list, func(i, j int) bool {
		if a, b := list[i].rx+list[i].tx, list[j].rx+list[j].tx; a != b {
			return a > b
		}
		return list[i].ni.RxBytes+list[i].ni.TxBytes > list[j].ni.RxBytes+list[j].ni.TxBytes
	})
	var out []string
	for i, e := range list {
		if i >= max {
			break
		}
		out = append(out, fmt.Sprintf("NET  %s ↓ %10s/s  ↑ %10s/s", tui.Pad(e.ni.Name, 12), humanBytes(e.rx), humanBytes(e.tx)))
	}
	if len(list) > max {
		out = append(out, tui.Dim(fmt.Sprintf("     +%d more interfaces — see Network tab", len(list)-max)))
	}
	return out
}

func (m *model) render(w, h int) []string {
	if w < 40 || h < 10 {
		return tooSmall(w, h)
	}
	cw := w
	if cw > 100 {
		cw = 100
	}
	innerW := cw - 4 // box side borders + padding
	innerH := h - 5  // header + tab bar + box top/bottom + footer

	if out, ok := m.renderOverlay(cw, innerW, innerH, w, h); ok {
		return out
	}

	var body []string
	title := tabNames[m.tab]
	switch m.tab {
	case tabOverview:
		body = m.renderOverview(innerW)
	case tabProcesses:
		body = m.proc.Render(innerW, innerH)
		title = fmt.Sprintf("Processes (%d)", len(m.snap.Procs))
	case tabDocker:
		body = m.dock.Render(innerW, innerH)
		if m.snap.DockerPresent {
			title = fmt.Sprintf("Docker (%d)", len(m.snap.Docker))
		}
	case tabNetwork:
		body = m.net.Render(innerW, innerH)
	case tabDisk:
		body = m.disk.Render(innerW, innerH)
		title = fmt.Sprintf("Disk (%d)", len(m.snap.Disks))
	}

	out := []string{m.headerBar(cw), tui.TabBar(tabNames, m.tab, cw)}
	out = append(out, tui.Box(title, body, cw, innerH)...)
	out = append(out, m.footer(cw))
	return tui.ClampAll(out, w, h)
}

// overlayFrame wraps a titled body between the header/tab bar and a footer line,
// clamped to the terminal — the common frame for every full-screen overlay.
func (m *model) overlayFrame(title string, body []string, footer string, cw, innerH, w, h int) []string {
	out := []string{m.headerBar(cw), tui.TabBar(tabNames, m.tab, cw)}
	out = append(out, tui.Box(title, body, cw, innerH)...)
	out = append(out, footer)
	return tui.ClampAll(out, w, h)
}

// renderOverlay draws a full-screen overlay when one is active (initial loading,
// a notice, or the detail/disk/stats views) and reports whether it did.
func (m *model) renderOverlay(cw, innerW, innerH, w, h int) ([]string, bool) {
	dim := func(s string) string { return tui.Dim(tui.ClampLine(s, cw)) }
	switch {
	case m.loading && !m.have:
		return m.overlayFrame("", []string{"", "  connecting…", ""}, dim("q quit"), cw, innerH, w, h), true
	case m.notice != "":
		return m.overlayFrame("Notice", []string{"", "  " + m.notice, ""}, dim("press any key to dismiss"), cw, innerH, w, h), true
	case m.detail != nil:
		body := m.renderDetailBody(innerW, innerH)
		return m.overlayFrame(m.detailTitle, body, m.detailFooter(cw), cw, innerH, w, h), true
	case m.diskExpl != nil:
		title, body := m.renderDiskExplorer(innerW, innerH)
		return m.overlayFrame(title, body, dim("j/k select · l/enter open · h/backspace up · . hidden · esc back"), cw, innerH, w, h), true
	case m.dockStats != nil:
		title, body := m.renderDockStats(innerW, innerH)
		return m.overlayFrame(title, body, dim("j/k select · c sort cpu · m sort mem · r reload · esc back"), cw, innerH, w, h), true
	case m.layoutEdit != nil:
		return m.overlayFrame("Customise Overview", m.renderLayoutEditor(), dim("j/k select · J/K move · space hide · w save · esc cancel"), cw, innerH, w, h), true
	}
	return nil, false
}

// headerBar is the top status line: identity on the left, clock + interval on
// the right, filling the width.
func (m *model) headerBar(w int) string {
	up := "—"
	if m.have {
		up = humanDuration(m.snap.UptimeSec)
	}
	alias, user, addr := m.srv.Alias, m.srv.User, m.srv.Addr()
	if m.anon {
		alias, user, addr = "server", "user", "demo.host"
	}
	left := fmt.Sprintf("kay · %s · %s@%s · up %s", alias, user, addr, up)
	right := fmt.Sprintf("%s · every %s", time.Now().Format("15:04:05"), m.interval)
	gap := w - tui.VisibleWidth(left) - tui.VisibleWidth(right) - 1
	if gap < 1 {
		return tui.Bold(tui.ClampLine(left, w))
	}
	return tui.Bold(left) + strings.Repeat(" ", gap+1) + tui.Dim(right)
}

func (m *model) renderOverview(width int) []string {
	if m.err != nil {
		return []string{"", tui.Red("⚠ collection error: " + tui.FirstLine(m.err.Error())), "", tui.Dim("retrying on next tick…")}
	}
	// A customised layout renders panels stacked in the user's order; the default
	// (uncustomised) layout keeps the two-column composition below.
	if m.overviewLayout != nil {
		return m.renderOverviewCustom()
	}
	s := m.snap
	var L []string

	// Wide terminals get a two-column top (system gauges | top processes);
	// narrow ones stack the same content.
	if width >= 88 {
		left := append([]string{tui.Cyan("System")}, m.overviewSystem(s)...)
		right := append([]string{tui.Cyan("Top processes")}, m.overviewProcs(s, 6)...)
		L = append(L, tui.Join(left, right, 4)...)
	} else {
		L = append(L, m.overviewSystem(s)...)
		L = append(L, "")
		L = append(L, m.overviewProcs(s, 3)...)
	}
	L = append(L, "")
	L = append(L, m.overviewNet(4)...) // busiest few; full list is on the Network tab
	L = append(L, "")
	L = append(L, m.overviewDocker(s))
	return L
}

func (m *model) overviewSystem(s metrics.Snapshot) []string {
	L := []string{
		gaugeLine("CPU", s.CPUPercent, 18, fmt.Sprintf("%d cores", s.NumCPU)),
		gaugeLine("MEM", s.MemUsedPercent, 18,
			fmt.Sprintf("%s / %s", humanKB(s.MemTotalKB-s.MemAvailableKB), humanKB(s.MemTotalKB))),
	}
	if d, ok := s.RootDisk(); ok {
		L = append(L, gaugeLine("DISK", d.UsedPercent(), 18,
			fmt.Sprintf("%s (%s)", d.Mount, humanBytes(float64(d.TotalBytes)))))
	}
	L = append(L, fmt.Sprintf("LOAD %s %.2f %.2f",
		loadColor(s.Load1, s.NumCPU, fmt.Sprintf("%.2f", s.Load1)), s.Load5, s.Load15))
	L = append(L, "cpu "+sparkline(m.cpuHist, 16))
	L = append(L, "mem "+sparkline(m.memHist, 16))
	return L
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// sparkline renders recent values (0..100) as a compact block-character trend,
// coloured by the latest value.
func sparkline(v []float64, width int) string {
	if len(v) == 0 {
		return tui.Dim("(collecting…)")
	}
	if len(v) > width {
		v = v[len(v)-width:]
	}
	var b strings.Builder
	for _, x := range v {
		if x < 0 {
			x = 0
		}
		if x > 100 {
			x = 100
		}
		b.WriteRune(sparkRunes[int(x/100*float64(len(sparkRunes)-1)+0.5)])
	}
	return tui.ThreshColor(b.String(), v[len(v)-1])
}

func (m *model) overviewProcs(s metrics.Snapshot, n int) []string {
	L := []string{tui.Cyan(fmt.Sprintf("%-7s %-15s %5s %5s", "PID", "COMMAND", "%CPU", "%MEM"))}
	for i, p := range s.Procs {
		if i >= n {
			break
		}
		name := p.Name
		if m.anon {
			name = fmt.Sprintf("proc-%d", i+1)
		}
		L = append(L, fmt.Sprintf("%-7d %-15s %5.1f %5.1f", p.PID, tui.Truncate(name, 15), p.CPU, p.Mem))
	}
	return L
}

func (m *model) overviewDocker(s metrics.Snapshot) string {
	switch {
	case !s.DockerPresent:
		return "DOCKER: not installed"
	case len(s.Docker) == 0:
		return "DOCKER: no running containers"
	default:
		healthy, unhealthy := 0, 0
		for _, c := range s.Docker {
			ls := strings.ToLower(c.Status)
			switch {
			case strings.Contains(ls, "unhealthy"):
				unhealthy++
			case strings.Contains(ls, "healthy"):
				healthy++
			}
		}
		msg := fmt.Sprintf("DOCKER: %d running", len(s.Docker))
		if healthy > 0 {
			msg += " · " + tui.Green(fmt.Sprintf("%d healthy", healthy))
		}
		if unhealthy > 0 {
			msg += " · " + tui.Red(fmt.Sprintf("%d unhealthy", unhealthy))
		}
		return msg
	}
}

func (m *model) footer(w int) string {
	if m.confirm != nil {
		return tui.Yellow(tui.ClampLine(m.confirm.text+"  [y/N]", w))
	}
	if m.status != "" {
		return tui.ClampLine(m.status, w)
	}
	return tui.Dim(tui.ClampLine(m.keyHints(), w))
}

func (m *model) blockedReadOnly() bool {
	if m.readOnly {
		m.status = tui.Yellow("read-only mode: destructive actions are disabled")
		return true
	}
	return false
}

func (m *model) keyHints() string {
	base := "Tab/H/L tabs · r refresh · +/- interval · q quit"
	if m.readOnly {
		base = tui.Yellow("[read-only]") + " " + base
	}
	switch m.tab {
	case tabProcesses:
		if m.readOnly {
			return "j/k select · s sort · Enter details · " + base
		}
		return "j/k select · s sort · x term · X kill · Enter details · " + base
	case tabDocker:
		if m.readOnly {
			return "j/k select · l logs · t stats · Enter inspect · " + base
		}
		return "j/k select · l logs · t stats · R restart · x stop · Enter inspect · " + base
	case tabDisk:
		return "j/k select · Enter/l explore (du) · " + base
	case tabNetwork:
		return "j/k select · " + base
	case tabOverview:
		return "o customise · " + base
	}
	return base
}

// renderDetailBody renders the visible pager rows with horizontal scrolling and
// search-match highlighting, marking the current match line.
func (m *model) renderDetailBody(width, height int) []string {
	start, end := m.detail.Window(height)
	cur := -1
	if len(m.searchHits) > 0 {
		cur = m.searchHits[m.searchIdx]
	}
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		text := highlightMatches(tui.HSlice(m.detail.Rows[i], m.detailHoff), m.searchQuery)
		marker := " "
		if i == cur {
			marker = tui.Yellow("▌")
		}
		out = append(out, marker+text)
	}
	return out
}

// highlightMatches wraps every case-insensitive occurrence of query in reverse
// video. The input line must be plain text.
func highlightMatches(line, query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return line
	}
	lower := strings.ToLower(line)
	lq := strings.ToLower(q)
	var b strings.Builder
	for i := 0; ; {
		j := strings.Index(lower[i:], lq)
		if j < 0 {
			b.WriteString(line[i:])
			break
		}
		j += i
		b.WriteString(line[i:j])
		b.WriteString(tui.Reverse(line[j : j+len(lq)]))
		i = j + len(lq)
	}
	return b.String()
}

func (m *model) detailFooter(w int) string {
	if m.searching {
		return tui.Yellow(tui.ClampLine("search: /"+m.searchQuery, w))
	}
	pos := ""
	if m.detail != nil && m.detail.Len() > 0 {
		pos = fmt.Sprintf("ln %d/%d", m.detail.Offset()+1, m.detail.Len())
	}
	if m.detailHoff > 0 {
		pos += fmt.Sprintf(" · col+%d", m.detailHoff)
	}
	hint := "j/k ↑↓ · h/l ←→ · g/G ends · / search · Esc/q back"
	if len(m.searchHits) > 0 {
		hint = fmt.Sprintf("match %d/%d · n/N · %s", m.searchIdx+1, len(m.searchHits), hint)
	}
	return tui.Dim(tui.ClampLine(pos+" · "+hint, w))
}

func tooSmall(w, h int) []string {
	return []string{"", "", fmt.Sprintf("  terminal too small — need >=40x10, have %dx%d", w, h)}
}
