// Package fleet renders a live, one-row-per-host overview of all registered
// servers, collecting metrics from each concurrently. Press Enter on a host to
// drill into its full dashboard and Esc/q to return (wired by cmd/kay via
// RunView); the overview itself is read-only.
package fleet

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/sshx"
	"github.com/Wigata-Intech/kay/internal/tui"

	"golang.org/x/term"
)

// Host is a server plus a function that opens a connection to it.
type Host struct {
	Server config.Server
	Dial   func() (*sshx.Client, error)
}

// Options configures the fleet view.
type Options struct {
	Interval  time.Duration
	Color     string
	Anonymize bool // mask aliases/hosts (for demos/screenshots)
}

type hostState struct {
	snap metrics.Snapshot
	err  error
	ok   bool
}

// screen is the subset of *tui.Screen the fleet loop needs. It is an interface
// so loop can be driven in tests without owning a real terminal.
type screen interface {
	Size() (int, int)
	Draw(lines []string)
}

// hostUpdate is one host's freshly collected state, streamed back to the loop as
// soon as that host finishes — so a slow host never blocks the others.
type hostUpdate struct {
	index int
	state hostState
}

// collector is the per-host capability the fleet needs: run a command (to
// collect metrics) and report connection state. *sshx.Managed satisfies it, so
// the fleet reuses one persistent connection per host instead of dialing every
// tick. It is an interface so the loop can be driven in tests with fakes.
type collector interface {
	metrics.Runner
	State() sshx.ConnState
	Err() error
}

// fleetView holds the mutable state of a running fleet overview.
type fleetView struct {
	hosts    []Host
	states   []hostState
	conns    []collector // one persistent, self-healing connection per host
	list     tui.List
	interval time.Duration
	anon     bool
	status   string // transient message (e.g. "still connecting"), cleared on next key
	help     bool   // true shows the key-binding overlay
	results  chan hostUpdate
	inflight int // hosts still collecting in the current round
}

// Selection is the host a user chose to drill into, together with its live
// persistent connection so the dashboard can reuse it — running its metrics over
// the same transport — instead of opening a second one. The connection keeps
// self-healing in the background, so the dashboard needs no separate redial.
type Selection struct {
	Host   Host
	Client metrics.Runner
}

// Run shows the fleet overview standalone: it owns the terminal and input for the
// whole session. Enter on a host has no effect here (drill-in needs a dashboard
// coordinator — see cmd/kay); use RunView for that.
func Run(hosts []Host, opts Options) error {
	if len(hosts) == 0 {
		return fmt.Errorf("no servers registered; add one with 'kay server add'")
	}
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	tui.SetColorMode(opts.Color)

	sess := NewSession(hosts)
	defer sess.Close()

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return runPlain(hosts, sess.conns, opts.Interval, opts.Anonymize)
	}

	scr, err := tui.NewScreen()
	if err != nil {
		return err
	}
	defer scr.Close()

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

	for {
		sel, err := sess.RunView(scr, events, opts)
		if err != nil {
			return err
		}
		if sel == nil {
			return nil // user quit
		}
		// Standalone has no dashboard to drill into; return to the overview.
	}
}

// dialCap bounds concurrent SSH dials across the whole fleet so a cold start of
// many hosts can't trip a server's MaxStartups throttle or exhaust local sockets.
const dialCap = 16

// Session owns one persistent, self-healing SSH connection per host and keeps
// them alive across repeated RunView calls, so drilling into a host's dashboard
// and back never re-handshakes. The caller (cmd/kay) creates one Session for the
// whole interactive session and Closes it at the end.
type Session struct {
	hosts []Host
	conns []collector
	pool  *sshx.Pool
}

// NewSession registers every host with a fresh connection pool and starts
// connecting to all of them in the background (dial concurrency is capped).
func NewSession(hosts []Host) *Session {
	pool := sshx.NewPool(dialCap)
	conns := make([]collector, len(hosts))
	for i := range hosts {
		conns[i] = pool.Add(hosts[i].Dial)
	}
	return &Session{hosts: hosts, conns: conns, pool: pool}
}

// Close tears down every connection in the pool.
func (s *Session) Close() { s.pool.Close() }

// RunView runs the fleet overview against an already-open screen and a shared
// input channel, reusing the session's persistent connections. It returns the
// host the user selected with Enter (with its live connection to reuse), or nil
// when they quit.
func (s *Session) RunView(scr *tui.Screen, events <-chan tui.Event, opts Options) (*Selection, error) {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	v := &fleetView{
		hosts:    s.hosts,
		states:   make([]hostState, len(s.hosts)),
		conns:    s.conns,
		interval: opts.Interval,
		anon:     opts.Anonymize,
		results:  make(chan hostUpdate, len(s.hosts)), // buffered so no host goroutine blocks
	}

	ticker := time.NewTicker(v.interval)
	defer ticker.Stop()

	v.trigger() // kick off the first collection before entering the loop
	return v.loop(scr, events, ticker.C, ticker), nil
}

// trigger starts a fresh round: one goroutine per host, each streaming its own
// result back as soon as it finishes. Collection reuses the persistent
// connection, so a healthy host pays no handshake; an unreachable one returns its
// connection state without blocking the others. Skips if a round is in flight.
func (v *fleetView) trigger() {
	if v.inflight > 0 {
		return
	}
	v.inflight = len(v.conns)
	for i := range v.conns {
		i, c := i, v.conns[i]
		go func() { v.results <- hostUpdate{index: i, state: collect(c)} }()
	}
}

// loop is the fleet's terminal-independent event loop: it redraws on every event
// and returns when the user quits (nil) or drills into a host (the selection).
// Per-host results stream in independently. The screen, input, and tick sources
// are injected so it can run headless in tests.
func (v *fleetView) loop(scr screen, events <-chan tui.Event, tick <-chan time.Time, ticker *time.Ticker) *Selection {
	draw := func() {
		w, h := scr.Size()
		if v.help {
			scr.Draw(renderFleetHelp(w, h))
			return
		}
		scr.Draw(render(v.hosts, v.states, &v.list, v.interval, v.status, w, h, v.anon))
	}
	draw()

	for {
		select {
		case ev := <-events:
			v.status = "" // clear any transient message on the next keypress
			switch {
			case v.help:
				v.help = false // any key closes the help overlay
			case ev.Rune == '?':
				v.help = true
			case ev.Key == tui.KeyEnter:
				if sel := v.enterHost(); sel != nil {
					return sel // drill into a ready host's dashboard
				}
			case handleFleetKey(ev, &v.list, &v.interval, ticker, v.trigger):
				return nil // quit
			}
			draw()
		case u := <-v.results:
			v.states[u.index] = u.state
			v.inflight--
			draw()
		case <-tick:
			v.trigger()
		}
	}
}

// enterHost returns the highlighted host with its live connection when it is
// ready to drill into; when it is still connecting or offline it sets a transient
// status instead and returns nil, so Enter on a not-ready host explains itself
// rather than doing nothing. Readiness is taken from the connection itself (not
// the last metrics round) so a just-connected host is immediately enterable.
func (v *fleetView) enterHost() *Selection {
	i := v.list.Selected
	if i < 0 || i >= len(v.conns) {
		return nil
	}
	c := v.conns[i]
	if c.State() == sshx.StateReady {
		return &Selection{Host: v.hosts[i], Client: c}
	}
	if c.Err() != nil {
		v.status = tui.Yellow("host is offline — can't open its dashboard")
	} else {
		v.status = tui.Yellow("host is still connecting — try again in a moment")
	}
	return nil
}

// handleFleetKey applies one input event to the fleet view. It returns true when
// the user asked to quit.
func handleFleetKey(ev tui.Event, list *tui.List, interval *time.Duration, ticker *time.Ticker, trigger func()) bool {
	switch {
	case ev.Type == tui.EventQuit, ev.Rune == 'q':
		return true
	case ev.Key == tui.KeyUp, ev.Rune == 'k':
		list.Move(-1)
	case ev.Key == tui.KeyDown, ev.Rune == 'j':
		list.Move(1)
	case ev.Key == tui.KeyHome, ev.Rune == 'g':
		list.Top()
	case ev.Key == tui.KeyEnd, ev.Rune == 'G':
		list.Bottom()
	case ev.Rune == 'r':
		trigger()
	case ev.Rune == '+':
		*interval += time.Second
		ticker.Reset(*interval)
	case ev.Rune == '-':
		if *interval > time.Second {
			*interval -= time.Second
			ticker.Reset(*interval)
		}
	}
	return false
}

// collect reads one host's current state from its persistent connection:
// freshly collected metrics when the connection is ready, otherwise the
// connection's own state (offline with the last error, or still connecting). It
// reuses the transport, so a healthy host pays no handshake.
func collect(c collector) hostState {
	switch c.State() {
	case sshx.StateReady:
		s, err := metrics.Collect(c)
		if err != nil {
			return hostState{err: err}
		}
		return hostState{snap: s, ok: true}
	case sshx.StateBroken:
		return hostState{err: c.Err()}
	default:
		return hostState{} // connecting: no data, no error yet
	}
}

// collectAll collects every host concurrently and returns once all finish. Used
// by the non-interactive plain renderer, which prints a full table each cycle.
func collectAll(conns []collector) []hostState {
	states := make([]hostState, len(conns))
	var wg sync.WaitGroup
	for i := range conns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			states[i] = collect(conns[i])
		}(i)
	}
	wg.Wait()
	return states
}

func render(hosts []Host, states []hostState, list *tui.List, interval time.Duration, status string, w, h int, anon bool) []string {
	if w < 40 || h < 8 {
		return []string{"", fmt.Sprintf("  terminal too small — need >=40x8, have %dx%d", w, h)}
	}
	cw := w
	if cw > 120 {
		cw = 120
	}
	innerW := cw - 4
	innerH := h - 4

	online := 0
	for _, st := range states {
		if st.ok {
			online++
		}
	}
	list.Header = fmt.Sprintf("%s %s %-8s %-8s %-8s %-6s %s",
		tui.Pad("ALIAS", 14), tui.Pad("HOST", 16), "CPU", "MEM", "DISK", "LOAD", "UPTIME")
	list.SetRows(rows(hosts, states, anon))

	out := []string{tui.Bold(tui.ClampLine(
		fmt.Sprintf("kay fleet · %s · every %s", time.Now().Format("15:04:05"), interval), cw))}
	out = append(out, tui.Box(fmt.Sprintf("Fleet — %d/%d online", online, len(hosts)),
		list.Render(innerW, innerH), cw, innerH)...)
	if status != "" {
		out = append(out, tui.ClampLine(status, cw))
	} else {
		out = append(out, tui.Dim(tui.ClampLine(
			"j/k select · Enter open host · r refresh · +/- interval · ? help · q quit", cw)))
	}
	return tui.ClampAll(out, w, h)
}

// fleetHelpSections is the key-binding reference shown by the `?` overlay.
func fleetHelpSections() []tui.HelpSection {
	return []tui.HelpSection{
		{Title: "Fleet", Keys: [][2]string{
			{"j/k ↑↓", "select host"}, {"g / G", "top / bottom"},
			{"Enter", "open host dashboard"}, {"r", "refresh now"},
			{"+ / -", "change interval"}, {"?", "this help"}, {"q", "quit"},
		}},
	}
}

// renderFleetHelp draws the full-screen key-binding overlay.
func renderFleetHelp(w, h int) []string {
	if w < 40 || h < 8 {
		return []string{"", fmt.Sprintf("  terminal too small — need >=40x8, have %dx%d", w, h)}
	}
	cw := w
	if cw > 120 {
		cw = 120
	}
	out := []string{tui.Bold(tui.ClampLine("kay fleet · keybindings", cw))}
	out = append(out, tui.Box("Keybindings", tui.RenderHelp(fleetHelpSections()), cw, h-4)...)
	out = append(out, tui.Dim(tui.ClampLine("press any key to close", cw)))
	return tui.ClampAll(out, w, h)
}

func rows(hosts []Host, states []hostState, anon bool) []string {
	out := make([]string, len(hosts))
	for i, hst := range hosts {
		aliasStr, hostStr := hst.Server.Alias, hst.Server.Host
		if anon {
			aliasStr = fmt.Sprintf("server-%d", i+1)
			hostStr = "demo.host"
		}
		alias := tui.Pad(aliasStr, 14)
		host := tui.Pad(hostStr, 16)
		st := states[i]
		switch {
		case st.err != nil:
			out[i] = fmt.Sprintf("%s %s %s", alias, host, tui.Red("offline: "+tui.FirstLine(st.err.Error())))
		case !st.ok:
			out[i] = fmt.Sprintf("%s %s %s", alias, host, tui.Dim("connecting…"))
		default:
			s := st.snap
			disk := 0.0
			if d, ok := s.RootDisk(); ok {
				disk = d.UsedPercent()
			}
			out[i] = fmt.Sprintf("%s %s %s %s %s  %-6.2f %s",
				alias, host,
				statCell("cpu", s.CPUPercent), statCell("mem", s.MemUsedPercent), statCell("dsk", disk),
				s.Load1, humanDurShort(s.UptimeSec))
		}
	}
	return out
}

func statCell(label string, pct float64) string {
	return tui.ThreshColor(fmt.Sprintf("%s %3.0f%%", label, pct), pct)
}

func runPlain(hosts []Host, conns []collector, interval time.Duration, anon bool) error {
	tui.ColorEnabled = false
	for {
		states := collectAll(conns)
		fmt.Printf("=== fleet · %s ===\n", time.Now().Format("15:04:05"))
		for i, hst := range hosts {
			alias, host := hst.Server.Alias, hst.Server.Host
			if anon {
				alias = fmt.Sprintf("server-%d", i+1)
				host = "demo.host"
			}
			st := states[i]
			if st.err != nil {
				fmt.Printf("  %-14s %-16s offline: %s\n", alias, host, tui.FirstLine(st.err.Error()))
				continue
			}
			s := st.snap
			disk := 0.0
			if d, ok := s.RootDisk(); ok {
				disk = d.UsedPercent()
			}
			fmt.Printf("  %-14s %-16s cpu %3.0f%% mem %3.0f%% dsk %3.0f%% load %.2f up %s\n",
				alias, host, s.CPUPercent, s.MemUsedPercent, disk, s.Load1, humanDurShort(s.UptimeSec))
		}
		time.Sleep(interval)
	}
}

func humanDurShort(sec float64) string {
	d := time.Duration(sec) * time.Second
	if days := int(d.Hours()) / 24; days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
