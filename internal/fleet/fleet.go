// Package fleet renders a live, one-row-per-host overview of all registered
// servers, collecting metrics from each concurrently. It is a read-only view;
// to manage a specific host, quit and run `kay dashboard --alias <alias>`.
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

// fleetView holds the mutable state of a running fleet overview.
type fleetView struct {
	hosts      []Host
	states     []hostState
	list       tui.List
	interval   time.Duration
	anon       bool
	results    chan []hostState
	collecting bool
}

// Run shows the fleet overview until the user quits. It owns the terminal
// lifecycle, then hands the wired input and tick sources to loop.
func Run(hosts []Host, opts Options) error {
	if len(hosts) == 0 {
		return fmt.Errorf("no servers registered; add one with 'kay server add'")
	}
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	tui.SetColorMode(opts.Color)

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return runPlain(hosts, opts.Interval, opts.Anonymize)
	}

	scr, err := tui.NewScreen()
	if err != nil {
		return err
	}
	defer scr.Close()

	v := &fleetView{
		hosts:    hosts,
		states:   make([]hostState, len(hosts)),
		interval: opts.Interval,
		anon:     opts.Anonymize,
		results:  make(chan []hostState, 1),
	}

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

	ticker := time.NewTicker(v.interval)
	defer ticker.Stop()

	v.trigger() // kick off the first collection before entering the loop
	return v.loop(scr, events, ticker.C, ticker)
}

// trigger starts a fleet collection unless one is already in flight.
func (v *fleetView) trigger() {
	if v.collecting {
		return
	}
	v.collecting = true
	hosts := v.hosts
	go func() { v.results <- collectAll(hosts) }()
}

// loop is the fleet's terminal-independent event loop: it redraws on every event
// and returns when the user quits. The screen, input, and tick sources are
// injected so it can run headless in tests. ticker is passed through to
// handleFleetKey for interval changes; loop reads ticks from tick.
func (v *fleetView) loop(scr screen, events <-chan tui.Event, tick <-chan time.Time, ticker *time.Ticker) error {
	draw := func() {
		w, h := scr.Size()
		scr.Draw(render(v.hosts, v.states, &v.list, v.interval, w, h, v.anon))
	}
	draw()

	for {
		select {
		case ev := <-events:
			if handleFleetKey(ev, &v.list, &v.interval, ticker, v.trigger) {
				return nil
			}
			draw()
		case st := <-v.results:
			v.collecting = false
			v.states = st
			draw()
		case <-tick:
			v.trigger()
		}
	}
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

// collectAll dials, collects, and closes every host concurrently.
func collectAll(hosts []Host) []hostState {
	states := make([]hostState, len(hosts))
	var wg sync.WaitGroup
	for i := range hosts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := hosts[i].Dial()
			if err != nil {
				states[i] = hostState{err: err}
				return
			}
			defer c.Close()
			s, cerr := metrics.Collect(c)
			if cerr != nil {
				states[i] = hostState{err: cerr}
				return
			}
			states[i] = hostState{snap: s, ok: true}
		}(i)
	}
	wg.Wait()
	return states
}

func render(hosts []Host, states []hostState, list *tui.List, interval time.Duration, w, h int, anon bool) []string {
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
	out = append(out, tui.Dim(tui.ClampLine(
		"j/k select · r refresh · +/- interval · q quit   (open a host: kay dashboard --alias …)", cw)))
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

func runPlain(hosts []Host, interval time.Duration, anon bool) error {
	tui.ColorEnabled = false
	for {
		states := collectAll(hosts)
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
