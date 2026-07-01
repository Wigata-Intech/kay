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
// return its combined output. It is an alias of metrics.Runner so the dashboard
// and the metrics collector share one seam (both satisfied by *sshx.Client).
type Client = metrics.Runner

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
	detail      *tui.Pager
	detailTitle string
	diskExpl    *diskExplorer // non-nil while drilling into a mount with du

	// detail-pager search + horizontal scroll state
	searching   bool
	searchQuery string
	searchHits  []int
	searchIdx   int
	detailHoff  int

	// event-loop runtime: created in Run and touched only from its goroutine,
	// so the flags need no synchronisation.
	results      chan collectResult
	reconnected  chan reconnectResult
	collecting   bool
	reconnecting bool
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

// screen is the subset of *tui.Screen the event loop needs. It is an interface
// so loop can be driven in tests without owning a real terminal.
type screen interface {
	Size() (int, int)
	Draw(lines []string)
}

// Run starts the dashboard. It owns the terminal lifecycle, then hands the wired
// input, signal, and tick sources to loop.
func Run(client Client, srv config.Server, opts Options) error {
	if opts.Interval <= 0 {
		opts.Interval = 3 * time.Second
	}
	tui.SetColorMode(opts.Color)

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
	// Collection runs in a goroutine and reports back on these channels so the
	// SSH round trip (and the remote CPU-sampling sleep) never blocks input.
	m.results = make(chan collectResult, 1)
	m.reconnected = make(chan reconnectResult, 1)
	m.refresh()

	events := make(chan tui.Event, 16)
	go readEvents(tui.NewReader(os.Stdin), events)

	sigCh := watchSignals()
	defer stopSignals(sigCh)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	return m.loop(scr, events, sigCh, ticker.C, func() { ticker.Reset(m.interval) })
}

// loop is the dashboard's terminal-independent event loop: it redraws on every
// event and returns when the user quits (a quit key or SIGTERM). The screen,
// input, signal, and tick sources are injected so it can run headless in tests.
func (m *model) loop(scr screen, events <-chan tui.Event, sigCh <-chan os.Signal, tick <-chan time.Time, resetTick func()) error {
	draw := func() {
		w, h := scr.Size()
		scr.Draw(m.render(w, h))
	}
	draw()

	for {
		select {
		case ev := <-events:
			if m.applyKeyEvent(ev, resetTick) {
				return nil
			}
			draw()
		case <-tick:
			m.trigger()
		case res := <-m.results:
			m.collecting = false
			m.applyCollect(res)
			draw()
		case rr := <-m.reconnected:
			m.reconnecting = false
			m.applyReconnect(rr)
			draw()
		case sig := <-sigCh:
			if signalIsQuit(sig) {
				return nil
			}
			draw() // resize or other: re-render at the current size
		}
	}
}

// readEvents pumps decoded input events onto ch until the reader errors.
func readEvents(reader *tui.Reader, ch chan<- tui.Event) {
	for {
		ev, err := reader.ReadEvent()
		ch <- ev
		if err != nil {
			return
		}
	}
}

// trigger starts a metrics collection unless one is already in flight.
func (m *model) trigger() {
	if m.collecting {
		return // a collection is already in flight; don't pile up
	}
	m.collecting = true
	cl := m.client // snapshot: reconnect may replace m.client later
	go func() {
		s, err := metrics.Collect(cl)
		m.results <- collectResult{snap: s, err: err}
	}()
}

// attemptReconnect kicks off a redial after a collection failure, if configured.
func (m *model) attemptReconnect() {
	if m.redial == nil || m.reconnecting {
		return
	}
	m.reconnecting = true
	m.status = tui.Yellow("connection lost — reconnecting…")
	go func() {
		nc, err := m.redial()
		m.reconnected <- reconnectResult{client: nc, err: err}
	}()
}

// applyKeyEvent handles one input event; it returns true when the loop should
// quit. resetTick is called when the refresh interval changes.
func (m *model) applyKeyEvent(ev tui.Event, resetTick func()) bool {
	if ev.Type == tui.EventQuit {
		return true
	}
	r := m.handleKey(ev)
	if r.quit {
		return true
	}
	if r.intervalChanged {
		resetTick()
	}
	if r.refreshNow {
		m.trigger()
	}
	return false
}

// applyCollect installs a collection result or starts a reconnect on failure.
func (m *model) applyCollect(res collectResult) {
	if res.err != nil {
		m.err = res.err
		m.attemptReconnect()
	} else {
		m.applySnap(res.snap)
	}
}

// applyReconnect swaps in a fresh client on success, or notes the retry.
func (m *model) applyReconnect(rr reconnectResult) {
	if rr.err == nil && rr.client != nil {
		m.client = rr.client
		m.status = tui.Green("reconnected")
		m.trigger() // fetch fresh data immediately
	} else {
		m.status = tui.Red("reconnect failed — retrying")
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
			tui.Pad(d.Mount, 22), makeBar(pct, 12), tui.ThreshColor(fmt.Sprintf("%5.1f%%", pct), pct),
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
