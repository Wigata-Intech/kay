// Package dashboard renders an interactive, tabbed terminal view of a remote
// host: Overview, Processes, Docker, and Network tabs with a moving cursor,
// selectable rows, and guarded actions (kill / docker logs, restart, stop).
// It is built on internal/tui and degrades to plain output when not a TTY.
package dashboard

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"

	"golang.org/x/term"
)

// Client is what the dashboard needs from an SSH connection: run a command and
// return its combined output. Satisfied by *sshx.Client.
type Client interface {
	Run(cmd string) (string, error)
}

// Options configures the dashboard.
type Options struct {
	Interval  time.Duration
	Color     string // "auto" | "always" | "never"
	ReadOnly  bool   // disable destructive actions (kill / restart / stop)
	Anonymize bool   // mask host/user/alias + Docker names (for demos/screenshots)

	// Redial, if set, is used to re-establish the connection after a failure.
	Redial func() (Client, error)
}

var tabNames = []string{"Overview", "Processes", "Docker", "Network", "Disk"}

const (
	tabOverview = iota
	tabProcesses
	tabDocker
	tabNetwork
	tabDisk
)

type confirmPrompt struct {
	text string
	run  func() string // performs the action, returns a status line
}

type model struct {
	srv      config.Server
	client   Client
	interval time.Duration

	snap   metrics.Snapshot
	prev   *metrics.Snapshot
	snapAt time.Time
	prevAt time.Time
	have   bool
	err    error

	tab      int
	readOnly bool
	anon     bool
	redial   func() (Client, error)
	sortMode int // process sort: 0=cpu 1=mem 2=pid 3=name
	cpuHist  []float64
	memHist  []float64
	proc     tui.List
	dock     tui.List
	net      tui.List
	disk     tui.List

	status      string
	confirm     *confirmPrompt
	detail      *tui.List
	detailTitle string

	// detail-pager search + horizontal scroll state
	searching   bool
	searchQuery string
	searchHits  []int
	searchIdx   int
	detailHoff  int
}

type keyResult struct {
	quit            bool
	refreshNow      bool
	intervalChanged bool
}

type collectResult struct {
	snap metrics.Snapshot
	err  error
}

type reconnectResult struct {
	client Client
	err    error
}

// Run starts the dashboard. It owns the terminal lifecycle and the event loop.
func Run(client Client, srv config.Server, opts Options) error {
	if opts.Interval <= 0 {
		opts.Interval = 3 * time.Second
	}
	setColor(opts.Color)

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return runPlain(client, srv, opts.Interval, opts.Anonymize)
	}

	enableVT() // Windows: enable ANSI processing; no-op elsewhere

	scr, err := tui.NewScreen()
	if err != nil {
		return err
	}
	defer scr.Close()

	m := &model{srv: srv, client: client, interval: opts.Interval, readOnly: opts.ReadOnly}
	m.redial = opts.Redial
	m.anon = opts.Anonymize
	m.refresh()

	events := make(chan tui.Event, 16)
	reader := tui.NewReader(os.Stdin)
	go func() {
		for {
			ev, err := reader.ReadEvent()
			events <- ev
			if err != nil {
				return
			}
		}
	}()

	sigCh := watchSignals()
	defer stopSignals(sigCh)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	// Collection runs in a goroutine and reports back on this channel so the
	// SSH round trip (and the remote CPU-sampling sleep) never blocks input.
	results := make(chan collectResult, 1)
	reconnected := make(chan reconnectResult, 1)
	collecting := false
	reconnecting := false
	trigger := func() {
		if collecting {
			return // a collection is already in flight; don't pile up
		}
		collecting = true
		cl := m.client // snapshot: reconnect may replace m.client later
		go func() {
			s, err := metrics.Collect(cl)
			results <- collectResult{snap: s, err: err}
		}()
	}
	attemptReconnect := func() {
		if m.redial == nil || reconnecting {
			return
		}
		reconnecting = true
		m.status = tui.Yellow("connection lost — reconnecting…")
		go func() {
			nc, err := m.redial()
			reconnected <- reconnectResult{client: nc, err: err}
		}()
	}

	draw := func() {
		w, h := scr.Size()
		scr.Draw(m.render(w, h))
	}
	draw()

	for {
		select {
		case ev := <-events:
			if ev.Type == tui.EventQuit {
				return nil
			}
			r := m.handleKey(ev)
			if r.quit {
				return nil
			}
			if r.intervalChanged {
				ticker.Reset(m.interval)
			}
			if r.refreshNow {
				trigger()
			}
			draw()
		case <-ticker.C:
			trigger()
		case res := <-results:
			collecting = false
			if res.err != nil {
				m.err = res.err
				attemptReconnect()
			} else {
				m.applySnap(res.snap)
			}
			draw()
		case rr := <-reconnected:
			reconnecting = false
			if rr.err == nil && rr.client != nil {
				m.client = rr.client
				m.status = tui.Green("reconnected")
				trigger() // fetch fresh data immediately
			} else {
				m.status = tui.Red("reconnect failed — retrying")
			}
			draw()
		case sig := <-sigCh:
			if signalIsQuit(sig) {
				return nil
			}
			draw() // resize or other: re-render at the current size
		}
	}
}

// ---- data refresh ----

func (m *model) refresh() {
	s, err := metrics.Collect(m.client)
	if err != nil {
		m.err = err
		return
	}
	m.applySnap(s)
}

// applySnap installs a freshly collected snapshot (called only from the event
// loop, so the model is never touched concurrently with the collect goroutine).
func (m *model) applySnap(s metrics.Snapshot) {
	m.err = nil
	if m.have {
		p := m.snap
		m.prev = &p
		m.prevAt = m.snapAt
	}
	m.snap = s
	m.snapAt = time.Now()
	m.have = true
	m.cpuHist = appendHist(m.cpuHist, s.CPUPercent, 120)
	m.memHist = appendHist(m.memHist, s.MemUsedPercent, 120)
	m.rebuildLists()
}

func appendHist(h []float64, v float64, max int) []float64 {
	h = append(h, v)
	if len(h) > max {
		h = h[len(h)-max:]
	}
	return h
}

func (m *model) rebuildLists() {
	s := m.snap
	sortProcs(s.Procs, m.sortMode)
	procRows := make([]string, 0, len(s.Procs))
	for i, p := range s.Procs {
		name := p.Name
		if m.anon {
			name = fmt.Sprintf("proc-%d", i+1)
		}
		procRows = append(procRows, fmt.Sprintf("%-7d %-18s %6.1f %6.1f",
			p.PID, tui.Truncate(name, 18), p.CPU, p.Mem))
	}
	m.proc.Header = fmt.Sprintf("%-7s %-18s %6s %6s", "PID", "COMMAND", "%CPU", "%MEM")
	m.proc.SetRows(procRows)

	dockRows := make([]string, 0, len(s.Docker))
	for i, c := range s.Docker {
		name, image := c.Name, c.Image
		if m.anon {
			name = fmt.Sprintf("service-%d", i+1)
			image = fmt.Sprintf("image-%d", i+1)
		}
		dockRows = append(dockRows, fmt.Sprintf("%-14s %-22s %s",
			tui.Truncate(name, 14), tui.Truncate(image, 22), colorStatus(c.Status)))
	}
	m.dock.Header = fmt.Sprintf("%-14s %-22s %s", "NAME", "IMAGE", "STATUS")
	m.dock.SetRows(dockRows)

	netRows := make([]string, 0, len(s.Net))
	for _, ni := range s.Net {
		rx, tx := m.netRate(ni)
		name := tui.Pad(ni.Name, 14)
		if rx > 0 || tx > 0 {
			name = tui.Green(name) // active interface
		}
		netRows = append(netRows, name+fmt.Sprintf(" ↓ %10s/s  ↑ %10s/s   rx %9s  tx %9s",
			humanBytes(rx), humanBytes(tx),
			humanBytes(float64(ni.RxBytes)), humanBytes(float64(ni.TxBytes))))
	}
	m.net.Header = tui.Pad("IFACE", 14) + "        DOWN             UP      TOTALS"
	m.net.SetRows(netRows)

	diskRows := make([]string, 0, len(s.Disks))
	for _, d := range s.Disks {
		pct := d.UsedPercent()
		diskRows = append(diskRows, fmt.Sprintf("%s %s %s  %9s / %-9s",
			tui.Pad(d.Mount, 22), makeBar(pct, 12), threshColor(fmt.Sprintf("%5.1f%%", pct), pct),
			humanBytes(float64(d.UsedBytes)), humanBytes(float64(d.TotalBytes))))
	}
	m.disk.Header = tui.Pad("MOUNT", 22) + " " + tui.Pad("USAGE", 14) + "  used / total"
	m.disk.SetRows(diskRows)
}

func sortProcs(p []metrics.Proc, mode int) {
	sort.SliceStable(p, func(i, j int) bool {
		switch mode {
		case 1:
			return p[i].Mem > p[j].Mem
		case 2:
			return p[i].PID < p[j].PID
		case 3:
			return strings.ToLower(p[i].Name) < strings.ToLower(p[j].Name)
		default:
			return p[i].CPU > p[j].CPU
		}
	})
}

func sortName(mode int) string {
	switch mode {
	case 1:
		return "MEM"
	case 2:
		return "PID"
	case 3:
		return "name"
	default:
		return "CPU"
	}
}

func (m *model) netRate(cur metrics.NetIface) (rx, tx float64) {
	if m.prev == nil {
		return 0, 0
	}
	dt := m.snapAt.Sub(m.prevAt).Seconds()
	if dt <= 0 {
		return 0, 0
	}
	for _, p := range m.prev.Net {
		if p.Name == cur.Name {
			if cur.RxBytes >= p.RxBytes {
				rx = float64(cur.RxBytes-p.RxBytes) / dt
			}
			if cur.TxBytes >= p.TxBytes {
				tx = float64(cur.TxBytes-p.TxBytes) / dt
			}
			return rx, tx
		}
	}
	return 0, 0
}

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

// ---- input handling ----

func (m *model) handleKey(ev tui.Event) keyResult {
	m.status = ""

	if m.detail != nil {
		return m.handleDetailKey(ev)
	}

	if m.confirm != nil {
		if ev.Rune == 'y' || ev.Rune == 'Y' {
			m.status = m.confirm.run()
		}
		m.confirm = nil
		return keyResult{refreshNow: true}
	}

	switch {
	case ev.Rune == 'q':
		return keyResult{quit: true}
	case ev.Key == tui.KeyTab:
		m.tab = (m.tab + 1) % len(tabNames)
		return keyResult{}
	case ev.Key == tui.KeyShiftTab, ev.Rune == '[':
		m.tab = (m.tab + len(tabNames) - 1) % len(tabNames)
		return keyResult{}
	case ev.Rune == ']':
		m.tab = (m.tab + 1) % len(tabNames)
		return keyResult{}
	case ev.Rune >= '1' && ev.Rune <= '5':
		m.tab = int(ev.Rune - '1')
		return keyResult{}
	case ev.Rune == 'r':
		return keyResult{refreshNow: true}
	case ev.Rune == '+':
		if m.interval < 60*time.Second {
			m.interval += time.Second
		}
		return keyResult{intervalChanged: true}
	case ev.Rune == '-':
		if m.interval > time.Second {
			m.interval -= time.Second
		}
		return keyResult{intervalChanged: true}
	}

	if l := m.activeList(); l != nil {
		switch {
		case ev.Key == tui.KeyUp, ev.Rune == 'k':
			l.Move(-1)
		case ev.Key == tui.KeyDown, ev.Rune == 'j':
			l.Move(1)
		case ev.Key == tui.KeyPgUp, ev.Key == tui.KeyCtrlU:
			l.Move(-10)
		case ev.Key == tui.KeyPgDn, ev.Key == tui.KeyCtrlD:
			l.Move(10)
		case ev.Key == tui.KeyHome, ev.Rune == 'g':
			l.Top()
		case ev.Key == tui.KeyEnd, ev.Rune == 'G':
			l.Bottom()
		}
	}

	switch m.tab {
	case tabProcesses:
		m.procAction(ev)
	case tabDocker:
		m.dockAction(ev)
	}
	return keyResult{}
}

func (m *model) activeList() *tui.List {
	switch m.tab {
	case tabProcesses:
		return &m.proc
	case tabDocker:
		return &m.dock
	case tabNetwork:
		return &m.net
	case tabDisk:
		return &m.disk
	}
	return nil
}

func (m *model) procAction(ev tui.Event) {
	if ev.Rune == 's' { // cycle sort key, keeping the selection on the same PID
		var pid int
		if m.proc.Selected >= 0 && m.proc.Selected < len(m.snap.Procs) {
			pid = m.snap.Procs[m.proc.Selected].PID
		}
		m.sortMode = (m.sortMode + 1) % 4
		m.rebuildLists()
		for i, p := range m.snap.Procs {
			if p.PID == pid {
				m.proc.Selected = i
				break
			}
		}
		m.status = tui.Dim("sorted by " + sortName(m.sortMode))
		return
	}
	if m.proc.Selected < 0 || m.proc.Selected >= len(m.snap.Procs) {
		return
	}
	p := m.snap.Procs[m.proc.Selected]
	switch {
	case ev.Key == tui.KeyEnter:
		out, _ := m.client.Run(fmt.Sprintf(
			"cat /proc/%d/status 2>/dev/null; echo; echo 'CMDLINE:'; tr '\\0' ' ' < /proc/%d/cmdline 2>/dev/null",
			p.PID, p.PID))
		m.openDetail(fmt.Sprintf("process %d (%s)", p.PID, p.Name), out)
	case ev.Rune == 'x':
		if m.blockedReadOnly() {
			return
		}
		pid, name := p.PID, p.Name
		m.confirm = &confirmPrompt{
			text: fmt.Sprintf("terminate %s (pid %d)?", name, pid),
			run:  func() string { return m.runAction(fmt.Sprintf("kill %d", pid), "sent SIGTERM to "+name) },
		}
	case ev.Rune == 'X':
		if m.blockedReadOnly() {
			return
		}
		pid, name := p.PID, p.Name
		m.confirm = &confirmPrompt{
			text: fmt.Sprintf("FORCE kill %s (pid %d)?", name, pid),
			run:  func() string { return m.runAction(fmt.Sprintf("kill -9 %d", pid), "sent SIGKILL to "+name) },
		}
	}
}

func (m *model) dockAction(ev tui.Event) {
	if m.dock.Selected < 0 || m.dock.Selected >= len(m.snap.Docker) {
		return
	}
	c := m.snap.Docker[m.dock.Selected]
	if !validID(c.ID) {
		return
	}
	switch {
	case ev.Key == tui.KeyEnter:
		out, _ := m.client.Run("docker inspect " + c.ID + " 2>&1")
		m.openDetail("inspect "+c.Name, out)
	case ev.Rune == 'l':
		out, _ := m.client.Run("docker logs --tail 200 " + c.ID + " 2>&1")
		m.openDetail("logs "+c.Name, out)
	case ev.Rune == 'R':
		if m.blockedReadOnly() {
			return
		}
		id, name := c.ID, c.Name
		m.confirm = &confirmPrompt{
			text: fmt.Sprintf("restart container %s?", name),
			run:  func() string { return m.runAction("docker restart "+id, "restarted "+name) },
		}
	case ev.Rune == 'x':
		if m.blockedReadOnly() {
			return
		}
		id, name := c.ID, c.Name
		m.confirm = &confirmPrompt{
			text: fmt.Sprintf("stop container %s?", name),
			run:  func() string { return m.runAction("docker stop "+id, "stopped "+name) },
		}
	}
}

func (m *model) runAction(cmd, okMsg string) string {
	out, err := m.client.Run(cmd)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err.Error()
		}
		return tui.Red("✗ " + firstLine(msg))
	}
	return tui.Green("✓ " + okMsg)
}

func (m *model) openDetail(title, content string) {
	lines := strings.Split(strings.ReplaceAll(content, "\t", "    "), "\n")
	m.detail = &tui.List{Rows: lines}
	m.detailTitle = title
	m.searching = false
	m.searchQuery = ""
	m.searchHits = nil
	m.searchIdx = 0
	m.detailHoff = 0
}

// handleDetailKey drives the scrollable, searchable detail/logs overlay.
func (m *model) handleDetailKey(ev tui.Event) keyResult {
	if m.searching {
		switch {
		case ev.Key == tui.KeyEnter:
			m.runSearch()
			m.searching = false
		case ev.Key == tui.KeyEsc:
			m.searching = false
			m.searchQuery = ""
		case ev.Key == tui.KeyBackspace:
			m.searchQuery = trimLastRune(m.searchQuery)
		case ev.Key == tui.KeyRune:
			m.searchQuery += string(ev.Rune)
		}
		return keyResult{}
	}

	switch {
	case ev.Key == tui.KeyUp, ev.Rune == 'k':
		m.detail.ScrollBy(-1)
	case ev.Key == tui.KeyDown, ev.Rune == 'j':
		m.detail.ScrollBy(1)
	case ev.Key == tui.KeyPgUp, ev.Key == tui.KeyCtrlU:
		m.detail.ScrollBy(-10)
	case ev.Key == tui.KeyPgDn, ev.Key == tui.KeyCtrlD:
		m.detail.ScrollBy(10)
	case ev.Rune == 'g', ev.Key == tui.KeyHome:
		m.detail.ScrollTop()
	case ev.Rune == 'G', ev.Key == tui.KeyEnd:
		m.detail.ScrollBottom()
	case ev.Key == tui.KeyLeft, ev.Rune == 'h':
		m.detailHoff -= 8
		if m.detailHoff < 0 {
			m.detailHoff = 0
		}
	case ev.Key == tui.KeyRight, ev.Rune == 'l':
		m.detailHoff += 8
	case ev.Rune == '/':
		m.searching = true
		m.searchQuery = ""
	case ev.Rune == 'n':
		m.jumpHit(1)
	case ev.Rune == 'N':
		m.jumpHit(-1)
	case ev.Key == tui.KeyEsc, ev.Rune == 'q':
		m.detail = nil
	}
	return keyResult{}
}

// runSearch finds all lines containing the query and jumps to the first.
func (m *model) runSearch() {
	q := strings.ToLower(strings.TrimSpace(m.searchQuery))
	m.searchHits = nil
	m.searchIdx = 0
	if q == "" || m.detail == nil {
		return
	}
	for i, line := range m.detail.Rows {
		if strings.Contains(strings.ToLower(line), q) {
			m.searchHits = append(m.searchHits, i)
		}
	}
	if len(m.searchHits) > 0 {
		m.detail.ScrollTo(m.searchHits[0])
	}
}

// jumpHit cycles to the next/previous search match.
func (m *model) jumpHit(d int) {
	if len(m.searchHits) == 0 {
		return
	}
	m.searchIdx = (m.searchIdx + d + len(m.searchHits)) % len(m.searchHits)
	m.detail.ScrollTo(m.searchHits[m.searchIdx])
}

func trimLastRune(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return string(r[:len(r)-1])
}

// ---- rendering ----

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

	if m.detail != nil {
		body := m.renderDetailBody(innerW, innerH)
		out := []string{m.headerBar(cw), tui.TabBar(tabNames, m.tab, cw)}
		out = append(out, tui.Box(m.detailTitle, body, cw, innerH)...)
		out = append(out, m.detailFooter(cw))
		return clampAll(out, w, h)
	}

	var body []string
	title := tabNames[m.tab]
	switch m.tab {
	case tabOverview:
		body = m.renderOverview(innerW)
	case tabProcesses:
		body = m.proc.Render(innerW, innerH, true)
		title = fmt.Sprintf("Processes (%d)", len(m.snap.Procs))
	case tabDocker:
		body = m.dock.Render(innerW, innerH, true)
		if m.snap.DockerPresent {
			title = fmt.Sprintf("Docker (%d)", len(m.snap.Docker))
		}
	case tabNetwork:
		body = m.net.Render(innerW, innerH, true)
	case tabDisk:
		body = m.disk.Render(innerW, innerH, true)
		title = fmt.Sprintf("Disk (%d)", len(m.snap.Disks))
	}

	out := []string{m.headerBar(cw), tui.TabBar(tabNames, m.tab, cw)}
	out = append(out, tui.Box(title, body, cw, innerH)...)
	out = append(out, m.footer(cw))
	return clampAll(out, w, h)
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
		return []string{"", tui.Red("⚠ collection error: " + firstLine(m.err.Error())), "", tui.Dim("retrying on next tick…")}
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
	return threshColor(b.String(), v[len(v)-1])
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
	base := "Tab/[ ] tabs · r refresh · +/- interval · q quit"
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
			return "j/k select · l logs · Enter inspect · " + base
		}
		return "j/k select · l logs · R restart · x stop · Enter inspect · " + base
	case tabNetwork, tabDisk:
		return "j/k select · " + base
	}
	return base
}

// renderDetailBody renders the visible pager rows with horizontal scrolling and
// search-match highlighting, marking the current match line.
func (m *model) renderDetailBody(width, height int) []string {
	start, end := m.detail.PagerWindow(height)
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

// ---- non-interactive fallback ----

func runPlain(client Client, srv config.Server, interval time.Duration, anon bool) error {
	tui.ColorEnabled = false
	alias := srv.Alias
	if anon {
		alias = "server"
	}
	for {
		s, err := metrics.Collect(client)
		fmt.Printf("=== %s · %s ===\n", alias, time.Now().Format("15:04:05"))
		host := s.Hostname
		if anon {
			host = "demo-host"
		}
		if err != nil {
			fmt.Println("  error:", err)
		} else {
			fmt.Printf("  %s · up %s · CPU %.1f%% · MEM %.1f%% · load %.2f\n",
				host, humanDuration(s.UptimeSec), s.CPUPercent, s.MemUsedPercent, s.Load1)
			for i, p := range s.Procs {
				if i >= 5 {
					break
				}
				name := p.Name
				if anon {
					name = fmt.Sprintf("proc-%d", i+1)
				}
				fmt.Printf("    %6d %-18s %5.1f%%cpu\n", p.PID, name, p.CPU)
			}
		}
		time.Sleep(interval)
	}
}

// ---- helpers ----

func setColor(mode string) {
	switch mode {
	case "always":
		tui.ColorEnabled = true
	case "never":
		tui.ColorEnabled = false
	default:
		tui.ColorEnabled = os.Getenv("NO_COLOR") == "" &&
			os.Getenv("TERM") != "dumb" &&
			term.IsTerminal(int(os.Stdout.Fd()))
	}
}

func tooSmall(w, h int) []string {
	return []string{"", "", fmt.Sprintf("  terminal too small — need >=40x10, have %dx%d", w, h)}
}

func clampAll(lines []string, w, h int) []string {
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = tui.ClampLine(lines[i], w)
	}
	return lines
}

// colorStatus colours a Docker status string by health/state.
func colorStatus(status string) string {
	ls := strings.ToLower(status)
	switch {
	case strings.Contains(ls, "unhealthy"):
		return tui.Red(status)
	case strings.Contains(ls, "healthy"):
		return tui.Green(status)
	case strings.HasPrefix(status, "Exited"), strings.Contains(ls, "dead"), strings.Contains(ls, "restarting"):
		return tui.Red(status)
	case strings.HasPrefix(status, "Up"):
		return tui.Green(status)
	}
	return status
}

func threshColor(s string, pct float64) string {
	switch {
	case pct >= 90:
		return tui.Red(s)
	case pct >= 70:
		return tui.Yellow(s)
	default:
		return tui.Green(s)
	}
}

func loadColor(load float64, ncpu int, s string) string {
	switch {
	case load > float64(ncpu):
		return tui.Red(s)
	case load > float64(ncpu)*0.7:
		return tui.Yellow(s)
	default:
		return tui.Green(s)
	}
}

func makeBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return "[" + threshColor(strings.Repeat("█", filled), pct) +
		tui.Dim(strings.Repeat("·", width-filled)) + "]"
}

func gaugeLine(label string, pct float64, width int, suffix string) string {
	return fmt.Sprintf("%-4s %s %s  %s",
		label, makeBar(pct, width), threshColor(fmt.Sprintf("%3.0f%%", pct), pct), suffix)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func validID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

func humanBytes(b float64) string {
	const unit = 1024.0
	if b < unit {
		return fmt.Sprintf("%.0f B", b)
	}
	v := b / unit
	for _, u := range []string{"K", "M", "G", "T", "P"} {
		if v < unit {
			return fmt.Sprintf("%.1f %sB", v, u)
		}
		v /= unit
	}
	return fmt.Sprintf("%.1f EB", v)
}

func humanKB(kb uint64) string { return humanBytes(float64(kb) * 1024) }

func humanDuration(sec float64) string {
	d := time.Duration(sec) * time.Second
	days := int(d.Hours()) / 24
	hh := int(d.Hours()) % 24
	mm := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hh, mm)
	}
	return fmt.Sprintf("%dh %dm", hh, mm)
}
