// Package app is the kay console: the view-stack router behind bare `kay` on
// a TTY. The fleet overview is its base view — drilling into a dashboard and
// back reuses one screen, one input reader, and one connection pool for the
// whole session — and management screens (forms, modals) are pushed on top of
// it as Views. Prompts raised by background dials (host trust, passphrases)
// arrive through the broker and surface as modals wherever the console is.
package app

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/dashboard"
	"github.com/Wigata-Intech/kay/internal/fleet"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// Screen is the subset of *tui.Screen the console needs. It is an interface
// so the whole console can be driven in tests without owning a terminal.
type Screen interface {
	Size() (int, int)
	Draw(lines []string)
}

// View is one pushed console screen. Draw gets the frame minus the statusbar
// line; Handle turns one input event into what the router should do next.
type View interface {
	Title() string
	Draw(w, h int) []string
	Handle(ev tui.Event) Action
}

// Action is a View's answer to an event: stay put, push another view, pop
// back, or quit the console. The zero value stays put.
type Action struct {
	kind actionKind
	next View
}

type actionKind int

const (
	actNone actionKind = iota
	actPush
	actPop
	actQuit
)

// None stays on the current view (the zero Action).
func None() Action { return Action{} }

// Push opens v on top of the current view.
func Push(v View) Action { return Action{kind: actPush, next: v} }

// Pop closes the current view, returning to the one beneath it.
func Pop() Action { return Action{kind: actPop} }

// Quit leaves the console.
func Quit() Action { return Action{kind: actQuit} }

// fleetSession is what the console needs from the fleet session; a seam so
// tests can drive the router without live SSH connections.
type fleetSession interface {
	RunView(scr fleet.Screen, events <-chan tui.Event, opts fleet.Options) (*fleet.Selection, error)
	Close()
}

var newFleetSession = func(hosts []fleet.Host) fleetSession { return fleet.NewSession(hosts) }

// runDashboard is a seam over dashboard.RunView so the mount path can be
// driven in tests without a live host.
var runDashboard = dashboard.RunView

// consoleKeys are the home-view keys the fleet loop hands back to the
// console. consoleHints is the curated footer — a handful of hints plus ?
// (the full map lives in the overlay); more than one line of hints is noise
// and overflows real terminals.
const (
	consoleKeys  = "aedicxK"
	consoleHints = "Enter open · c connect · x exec · a add · ? keys · q quit"
)

// consoleHelp is the console's section in the fleet's ? overlay — the full
// key map behind the curated footer.
func consoleHelp() []tui.HelpSection {
	return []tui.HelpSection{
		{Title: "Console", Keys: [][2]string{
			{"a / e / d", "add / edit / delete server"},
			{"i", "install key on the host"},
			{"c", "connect (interactive shell)"},
			{"x", "run a command"},
			{"K", "manage keys"},
			{"Ctrl-C", "quit from anywhere"},
		}},
	}
}

// hinter is the optional View extension supplying the bottom bar's
// contextual hints (right side); views without it fall back to the quit hint.
type hinter interface{ Hints() string }

// frameRecorder remembers the last frame drawn through it, so modal views
// pushed later can dim and overlay the screen they interrupt.
type frameRecorder struct {
	Screen
	last []string
}

func (r *frameRecorder) Draw(lines []string) {
	r.last = lines
	r.Screen.Draw(lines)
}

// Suspend/Resume delegate to the wrapped screen when it supports the
// handoff, so the recorder stays transparent to connect.
func (r *frameRecorder) Suspend() {
	if s, ok := r.Screen.(suspender); ok {
		s.Suspend()
	}
}

func (r *frameRecorder) Resume() error {
	if s, ok := r.Screen.(suspender); ok {
		return s.Resume()
	}
	return nil
}

// overlayBase carries the frame a modal draws over, captured at push time so
// stacked modals keep a stable backdrop.
type overlayBase struct{ base []string }

func (o *overlayBase) setBase(b []string) { o.base = b }

// Console runs the interactive session. The caller owns the screen and the
// input reader (they outlive every view); the console owns the store, the
// fleet session lifecycle, and the prompt broker.
type Console struct {
	store      *config.Store
	buildHosts func() []fleet.Host
	fleetOpts  fleet.Options
	readOnly   bool

	stack      []View
	status     string         // transient message shown in the statusbar, cleared on the next key
	hostsDirty bool           // a server change invalidated the fleet session
	rec        *frameRecorder // set by Run; modal backdrops come from its last frame

	// Connect, when set, dials the server and returns the interactive-shell
	// handoff (run while the screen is suspended) plus a cleanup for the
	// dialed connection. The console runs the dial off the UI goroutine, so
	// prompts it raises (host trust, passphrases) can surface as modals.
	// InstallKey, when set, installs the server's public key over a
	// password login. Both are wired by cmd/kay, which owns the SSH glue.
	Connect    func(srv config.Server) (shell func() error, cleanup func(), err error)
	InstallKey func(srv config.Server, password string) error

	mu    sync.Mutex
	queue []func()      // closures for the UI goroutine, raised by other goroutines
	wake  chan struct{} // doorbell for queue arrivals; doubles as the fleet Interrupt
	done  chan struct{} // closed when Run exits, releasing blocked askers
}

// NewConsole builds a console over the store. buildHosts is re-invoked after
// every server change so the fleet session always matches the store.
func NewConsole(st *config.Store, buildHosts func() []fleet.Host, opts fleet.Options, readOnly bool) *Console {
	return &Console{
		store:      st,
		buildHosts: buildHosts,
		fleetOpts:  opts,
		readOnly:   readOnly,
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
}

// Run drives the console until the user quits: the fleet overview as the base
// loop, Enter mounting the selected host's dashboard, console keys opening
// management views, and the welcome view standing in while no servers exist.
func (c *Console) Run(scr Screen, events <-chan tui.Event) error {
	defer close(c.done) // release any prompt still blocked in Ask*
	rec := &frameRecorder{Screen: scr}
	scr = rec
	c.rec = rec
	for {
		hosts := c.buildHosts()
		if len(hosts) == 0 {
			c.Push(&welcomeView{c: c})
			if quit := c.drive(scr, events); quit {
				return nil
			}
			if !c.takeHostsDirty() {
				return nil // stack drained without changes: nothing left to show
			}
			continue
		}
		quit, err := c.fleetLoop(scr, events, hosts)
		if err != nil || quit {
			return err
		}
		// A server change invalidated the session: rebuild from the store.
	}
}

// fleetLoop runs one fleet session until the user quits, an error occurs, or
// the host set changes (server add/edit/delete rebuilds the session).
func (c *Console) fleetLoop(scr Screen, events <-chan tui.Event, hosts []fleet.Host) (quit bool, err error) {
	sess := newFleetSession(hosts)
	defer sess.Close()

	opts := c.fleetOpts
	opts.ConsoleKeys = consoleKeys
	opts.Interrupt = c.wake
	opts.FooterHints = consoleHints
	opts.ExtraHelp = consoleHelp()

	for {
		// Hand any pending transient status to the fleet frame — actions
		// that end back here (a refused dial, x on an offline host) must
		// explain themselves where the user actually looks.
		opts.Status = c.status
		c.status = ""
		sel, err := sess.RunView(scr, events, opts)
		if err != nil {
			return false, err
		}
		if sel == nil {
			return true, nil // user quit the fleet
		}
		switch {
		case sel.Interrupted:
			c.runPosted()
			if c.drive(scr, events) {
				return true, nil
			}
		case sel.Key == 'c':
			quit, cerr := c.connect(scr, events, sel.Host.Server)
			if cerr != nil {
				return false, cerr
			}
			if quit {
				return true, nil
			}
		case sel.Key != 0:
			if v := c.viewFor(sel); v != nil {
				c.Push(v)
				if c.drive(scr, events) {
					return true, nil
				}
			}
		default:
			// The dashboard owns events until it returns, so broker prompts
			// raised meanwhile wait (the wake token persists) — deliberate:
			// a modal must not fight the dashboard for the frame.
			exitApp, derr := runDashboard(scr, events, sel.Client, sel.Host.Server, c.dashboardOpts())
			if derr != nil {
				return false, derr
			}
			if exitApp {
				return true, nil
			}
			// q / Esc in the dashboard: back to the fleet overview.
		}
		if c.takeHostsDirty() {
			return false, nil
		}
	}
}

// anon reports whether demo redaction is on (fleet rows already honor it; the
// management views mask through the helpers below).
func (c *Console) anon() bool { return c.fleetOpts.Anonymize }

// maskAlias returns the fleet-style stand-in for a server alias ("server-N",
// matching the row the fleet shows for it).
func (c *Console) maskAlias(alias string) string {
	for i := range c.store.Servers {
		if c.store.Servers[i].Alias == alias {
			return fmt.Sprintf("server-%d", i+1)
		}
	}
	return "server"
}

// maskKey returns the CLI-consistent stand-in for a key name ("key-N", as
// `kay key ls` shows under KAY_DEMO).
func (c *Console) maskKey(name string) string {
	for i := range c.store.Keys {
		if c.store.Keys[i].Name == name {
			return fmt.Sprintf("key-%d", i+1)
		}
	}
	return "key"
}

// viewFor maps a console key from the home view to its management screen; nil
// (with a status explaining why) when the action is not available.
func (c *Console) viewFor(sel *fleet.Selection) View {
	switch sel.Key {
	case 'a':
		return c.addServerView()
	case 'e':
		return c.editServerView(sel.Host.Server)
	case 'd':
		return c.deleteServerView(sel.Host.Server)
	case 'i':
		return c.installView(sel.Host.Server)
	case 'x':
		if sel.Client == nil {
			c.status = tui.Yellow("host is not connected — can't run a command")
			return nil
		}
		return c.execView(sel.Host.Server, sel.Client)
	default: // 'K'
		return c.keysView()
	}
}

// suspender is the optional Screen extension the connect handoff needs;
// *tui.Screen implements it, test fakes may not.
type suspender interface {
	Suspend()
	Resume() error
}

// connect dials off the UI goroutine — pumping broker prompts and swallowing
// typed-ahead keys meanwhile, so a dial that asks a question can never block
// the goroutine that must answer it — then suspends the screen and hands the
// terminal to the shell. Dial and shell errors land on the statusbar; quit
// reports Ctrl-C during the dial; err is fatal (Resume failed — without the
// raw alternate screen the console cannot continue).
func (c *Console) connect(scr Screen, events <-chan tui.Event, srv config.Server) (quit bool, err error) {
	if c.Connect == nil {
		return false, nil
	}
	scr.Draw([]string{tui.Dim("connecting to " + srv.Alias + "…")})

	type result struct {
		shell   func() error
		cleanup func()
		err     error
	}
	res := make(chan result, 1)
	go func() {
		sh, cl, derr := c.Connect(srv)
		res <- result{shell: sh, cleanup: cl, err: derr}
	}()
	// A quit abandons the dial; its connection (if it lands) is released in
	// the background so nothing leaks past Run.
	abandon := func() bool {
		go func() {
			if r := <-res; r.cleanup != nil {
				r.cleanup()
			}
		}()
		return true
	}

	var r result
	for waiting := true; waiting; {
		select {
		case r = <-res:
			waiting = false
		case <-c.wake:
			c.runPosted()
			if c.drive(scr, events) {
				return abandon(), nil
			}
		case ev, ok := <-events:
			if !ok || ev.Type == tui.EventQuit {
				return abandon(), nil
			}
			// Swallow typed-ahead keys: aimed at neither the fleet nor the shell.
		}
	}
	if r.err != nil {
		c.status = tui.Red(r.err.Error())
		return false, nil
	}
	defer r.cleanup()

	sus, ok := scr.(suspender)
	if ok {
		sus.Suspend()
	}
	if serr := r.shell(); serr != nil {
		c.status = tui.Red(serr.Error())
	}
	if ok {
		if rerr := sus.Resume(); rerr != nil {
			return false, fmt.Errorf("cannot re-enter the console screen: %w", rerr)
		}
	}
	return false, nil
}

// dashboardOpts assembles the drill-in dashboard options from the console's
// settings and the store's saved Overview layout.
func (c *Console) dashboardOpts() dashboard.Options {
	return dashboard.Options{
		Interval:  c.fleetOpts.Interval,
		Color:     c.fleetOpts.Color,
		ReadOnly:  c.readOnly,
		Anonymize: c.fleetOpts.Anonymize,
		Overview:  c.store.OverviewPanels(),
		SaveLayout: func(p []config.PanelPref) error {
			c.store.SetOverviewPanels(p)
			return c.store.Save()
		},
		// No Redial: the reused connection is pool-managed and self-heals.
	}
}

// Push adds v to the view stack; the next drive shows it on top. Modal views
// get the current frame as their backdrop.
func (c *Console) Push(v View) {
	if o, ok := v.(interface{ setBase([]string) }); ok && c.rec != nil {
		o.setBase(c.rec.last)
	}
	c.stack = append(c.stack, v)
}

// markHostsDirty records that the fleet session no longer matches the store.
func (c *Console) markHostsDirty() { c.hostsDirty = true }

// takeHostsDirty consumes the dirty flag.
func (c *Console) takeHostsDirty() bool {
	d := c.hostsDirty
	c.hostsDirty = false
	return d
}

// drive runs the pushed views until the stack empties (back to the base view,
// returns false) or a view quits the console (returns true). Ctrl-C quits
// from any view, broker prompts surface on top, and a server change clears
// the stack so the base loop can rebuild. The last frame line is the
// statusbar; views draw above it.
func (c *Console) drive(scr Screen, events <-chan tui.Event) (quit bool) {
	for len(c.stack) > 0 {
		top := c.stack[len(c.stack)-1]
		w, h := scr.Size()
		if h < 2 {
			h = 2 // one content line plus the statusbar
		}
		// Copy into a fresh frame: appending to the view's slice could write
		// into its backing array.
		frame := make([]string, h)
		copy(frame[:h-1], top.Draw(w, h-1))
		frame[h-1] = c.statusBar(w)
		scr.Draw(frame)

		var ev tui.Event
		var ok bool
		select {
		case ev, ok = <-events:
			if !ok {
				return false // input reader gone: nothing left to drive
			}
		case <-c.wake:
			c.runPosted()
			continue
		}
		if ev.Type == tui.EventQuit {
			return true
		}
		c.status = ""
		switch act := top.Handle(ev); act.kind {
		case actPush:
			c.Push(act.next) // through Push so modals get their backdrop
		case actPop:
			c.stack = c.stack[:len(c.stack)-1]
		case actQuit:
			return true
		}
		if c.hostsDirty {
			// Prompts always sit above the view that set the flag, so nothing
			// pending is dropped with the rest of the stack.
			c.stack = nil
			return false
		}
	}
	return false
}

// statusBar renders the console's one bottom bar: the transient status when
// set (otherwise the breadcrumb of open views) on the left, and the top
// view's contextual hints on the right.
func (c *Console) statusBar(w int) string {
	left := c.status
	if left == "" {
		parts := make([]string, 0, len(c.stack)+1)
		parts = append(parts, "kay")
		for _, v := range c.stack {
			parts = append(parts, v.Title())
		}
		left = strings.Join(parts, " › ")
	}
	right := "Ctrl-C quit"
	if len(c.stack) > 0 {
		if h, ok := c.stack[len(c.stack)-1].(hinter); ok && h.Hints() != "" {
			right = h.Hints()
		}
	}
	return tui.StatusBar(tui.Dim(left), tui.Dim(right), w)
}

// welcomeView greets a console with no servers registered yet.
type welcomeView struct{ c *Console }

func (*welcomeView) Title() string { return "servers" }

func (*welcomeView) Draw(w, h int) []string {
	cw := w
	if cw > 120 {
		cw = 120
	}
	body := []string{
		"",
		"  No servers registered.",
		"",
		"  Press " + tui.Bold("K") + " to generate an SSH key,",
		"  then " + tui.Bold("a") + " to add your first server.",
		"",
	}
	out := []string{}
	out = append(out, tui.Box("Servers", body, cw, len(body))...)
	return tui.ClampAll(out, w, h)
}

func (*welcomeView) Hints() string { return "a add server · K keys · q quit" }

func (v *welcomeView) Handle(ev tui.Event) Action {
	switch {
	case ev.Rune == 'a':
		return Push(v.c.addServerView())
	case ev.Rune == 'K':
		return Push(v.c.keysView())
	case ev.Rune == 'q', ev.Key == tui.KeyEsc:
		return Quit()
	}
	return None()
}
