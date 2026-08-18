// Command kay is a small CLI for managing a fleet of Linux servers over SSH:
// generate keys, register servers, install keys, run commands, and watch a
// refreshing metrics dashboard.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/dashboard"
	"github.com/Wigata-Intech/kay/internal/fleet"
	"github.com/Wigata-Intech/kay/internal/keys"
	"github.com/Wigata-Intech/kay/internal/tui"

	sshx "github.com/Wigata-Intech/w-tools/x/sshx"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// Set at build time via -ldflags (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// Test seams: process-level effects and terminal reads injected so the
// dispatch and prompt paths are coverable without a TTY or a real exit.
var (
	exit                   = os.Exit
	stdinReader  io.Reader = os.Stdin
	stdinFile              = os.Stdin
	readPassword           = term.ReadPassword
	isTerminal             = term.IsTerminal
	dashboardRun           = dashboard.Run
	fleetRun               = fleet.Run
	newScreen              = func() (uiScreen, error) { return tui.NewScreen() }
	vcsStamp               = func() (rev, when string, ok bool) {
		bi, _ := debug.ReadBuildInfo()
		return stampFrom(bi)
	}
)

// uiScreen is what fleetDrill needs from a terminal screen; *tui.Screen
// satisfies it, and tests drive the drill loop with a fake.
type uiScreen interface {
	Size() (int, int)
	Draw(lines []string)
	Close()
}

// handler runs a subcommand over its remaining args.
type handler func([]string) error

// handlers maps each subcommand to its implementation. version/help are handled
// separately in main because they take no args and print directly.
var handlers = map[string]handler{
	"key":       cmdKey,
	"server":    cmdServer,
	"install":   cmdInstall,
	"connect":   cmdConnect,
	"exec":      cmdExec,
	"dashboard": cmdDashboard,
	"fleet":     cmdFleet,
	"ls":        cmdLs,
}

func main() {
	if len(os.Args) < 2 {
		// Interactive terminal: open the console. Pipes and scripts keep the
		// usage-and-exit contract unchanged.
		if isTerminal(int(os.Stdin.Fd())) {
			if err := runConsole(); err != nil {
				fmt.Fprintln(os.Stderr, "kay: "+err.Error())
				exit(1)
			}
			return
		}
		usage()
		exit(2)
		return
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "version", "-v", "--version":
		cmdVersion()
		return
	case "help", "-h", "--help":
		usage()
		return
	}

	h, ok := handlers[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "kay: unknown command %q\n", cmd)
		exit(1)
		return
	}
	if err := h(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return // `-h`/`--help` on a subcommand: flag already printed its usage
		}
		fmt.Fprintln(os.Stderr, "kay: "+err.Error())
		exit(1)
	}
}

// cmdVersion prints the build version with optional commit/date. Release builds
// set these via ldflags; for local `go build`/`make build` (no ldflags) it falls
// back to the VCS stamp Go embeds in the binary, so `kay version` is never bare.
func cmdVersion() {
	v, c, d := version, commit, date
	if c == "" || d == "" {
		if rev, when, ok := vcsStamp(); ok {
			if c == "" {
				c = rev
			}
			if d == "" {
				d = when
			}
		}
	}
	var extra []string
	if c != "" {
		extra = append(extra, c)
	}
	if d != "" {
		extra = append(extra, d)
	}
	if len(extra) > 0 {
		v += " (" + strings.Join(extra, ", ") + ")"
	}
	fmt.Println("kay " + v)
}

// vcsStamp reads the VCS revision/time Go embeds during `go build` in a git
// checkout, used when ldflags didn't supply commit/date. The revision is
// shortened and marked "-dirty" when the working tree had uncommitted changes.
// stampFrom extracts the shortened, dirty-marked VCS stamp from build info,
// tolerating a nil info (stripped binaries).
func stampFrom(bi *debug.BuildInfo) (rev, when string, ok bool) {
	if bi == nil {
		return "", "", false
	}
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
			if len(rev) > 12 {
				rev = rev[:12]
			}
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty && rev != "" {
		rev += "-dirty"
	}
	return rev, when, rev != "" || when != ""
}

func usage() {
	fmt.Print(`kay - manage Linux servers over SSH

Usage:
  kay key gen --name NAME [--type ed25519|rsa] [--bits 3072]
  kay key ls
  kay key show --name NAME
  kay server add --alias A --host H [--port 22] --user U --key NAME
  kay server ls
  kay server rm --alias A
  kay install --alias A [--push]
  kay connect [--alias A] [--insecure]
  kay exec [--alias A] [--insecure] -- CMD...
  kay dashboard [--alias A] [--interval 3s] [--insecure] [--read-only] [--anonymize] [--color auto|always|never]
  kay fleet [--interval 5s] [--insecure] [--anonymize] [--color auto|always|never]
  kay ls
  kay version

Examples:
  kay key gen --name laptop                         generate a key
  kay server add --alias web --host 10.0.0.1 --user ubuntu --key laptop
  kay install --alias web --push                    install the key over a password login
  kay dashboard --alias web                         watch one host (press ? for keys)
  kay fleet                                          watch every host; Enter drills in

Run any subcommand with -h for its flags.
`)
}

// ---- key ----

func cmdKey(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kay key <gen|ls|show>")
	}
	sub, rest := args[0], args[1:]
	st, err := config.Load()
	if err != nil {
		return err
	}
	switch sub {
	case "gen":
		return cmdKeyGen(st, rest)
	case "ls":
		return cmdKeyLs(st)
	case "show":
		return cmdKeyShow(st, rest)
	}
	return fmt.Errorf("unknown key subcommand %q", sub)
}

func cmdKeyGen(st *config.Store, rest []string) error {
	fs := flag.NewFlagSet("key gen", flag.ContinueOnError)
	name := fs.String("name", "", "key name (required)")
	typ := fs.String("type", "ed25519", "key type: ed25519 or rsa")
	bits := fs.Int("bits", 3072, "rsa key size in bits")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	pair, err := keys.Generate(config.KeyType(*typ), *bits, *name)
	if err != nil {
		return err
	}
	privPath, pubPath, err := pair.Write(st.KeysDir(), *name)
	if err != nil {
		return err
	}
	if err := st.AddKey(config.Key{
		Name: *name, Type: config.KeyType(*typ),
		PrivatePath: privPath, PublicPath: pubPath,
		Fingerprint: pair.Fingerprint, CreatedAt: time.Now(),
	}); err != nil {
		return err
	}
	if err := st.Save(); err != nil {
		return err
	}
	fmt.Printf("created key %q (%s)\n  %s\n  public: %s\n", *name, *typ, pair.Fingerprint, pubPath)
	return nil
}

func cmdKeyLs(st *config.Store) error {
	anon := anonEnabled()
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tFINGERPRINT\tCREATED")
	for i, k := range st.Keys {
		name, fp := k.Name, k.Fingerprint
		if anon {
			name, fp = fmt.Sprintf("key-%d", i+1), "SHA256:…"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, k.Type, fp, k.CreatedAt.Format("2006-01-02"))
	}
	return w.Flush()
}

func cmdKeyShow(st *config.Store, rest []string) error {
	fs := flag.NewFlagSet("key show", flag.ContinueOnError)
	name := fs.String("name", "", "key name")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	k, err := st.FindKey(*name)
	if err != nil {
		return err
	}
	pub, err := keys.ReadPublic(k.PublicPath)
	if err != nil {
		return err
	}
	fmt.Print(pub)
	return nil
}

// ---- server ----

func cmdServer(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kay server <add|ls|rm>")
	}
	sub, rest := args[0], args[1:]
	st, err := config.Load()
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		return cmdServerAdd(st, rest)
	case "ls":
		return cmdServerLs(st)
	case "rm":
		return cmdServerRm(st, rest)
	}
	return fmt.Errorf("unknown server subcommand %q", sub)
}

func cmdServerAdd(st *config.Store, rest []string) error {
	fs := flag.NewFlagSet("server add", flag.ContinueOnError)
	alias := fs.String("alias", "", "unique alias (required)")
	host := fs.String("host", "", "host or IP (required)")
	port := fs.Int("port", 22, "ssh port")
	user := fs.String("user", "", "login user (required)")
	key := fs.String("key", "", "key name (required)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *alias == "" || *host == "" || *user == "" || *key == "" {
		return fmt.Errorf("--alias, --host, --user and --key are required")
	}
	if err := st.AddServer(config.Server{
		Alias: *alias, Host: *host, Port: *port, User: *user, KeyName: *key,
	}); err != nil {
		return err
	}
	if err := st.Save(); err != nil {
		return err
	}
	fmt.Printf("added server %q -> %s@%s:%d (key %s)\n", *alias, *user, *host, *port, *key)
	return nil
}

func cmdServerLs(st *config.Store) error {
	anon := anonEnabled()
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tHOST\tPORT\tUSER\tKEY")
	for i, s := range st.Servers {
		alias, host, user, keyn := s.Alias, s.Host, s.User, s.KeyName
		if anon {
			alias, host, user, keyn = fmt.Sprintf("server-%d", i+1), "demo.host", "user", "key"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", alias, host, s.Port, user, keyn)
	}
	return w.Flush()
}

func cmdServerRm(st *config.Store, rest []string) error {
	fs := flag.NewFlagSet("server rm", flag.ContinueOnError)
	alias := fs.String("alias", "", "alias to remove")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if err := st.RemoveServer(*alias); err != nil {
		return err
	}
	if err := st.Save(); err != nil {
		return err
	}
	fmt.Printf("removed server %q\n", *alias)
	return nil
}

// ---- install ----

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	alias := fs.String("alias", "", "server alias")
	push := fs.Bool("push", false, "install the key now over a password SSH login")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := config.Load()
	if err != nil {
		return err
	}
	srv, err := pickServer(st, *alias)
	if err != nil {
		return err
	}
	k, err := st.FindKey(srv.KeyName)
	if err != nil {
		return err
	}
	pub, err := keys.ReadPublic(k.PublicPath)
	if err != nil {
		return err
	}
	pub = strings.TrimSpace(pub)

	if *push {
		fmt.Fprintf(os.Stderr, "Password for %s@%s: ", srv.User, srv.Host)
		pw, rerr := readPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if rerr != nil {
			return rerr
		}
		if err := pushKey(st, srv, pub, string(pw)); err != nil {
			return err
		}
		fmt.Printf("installed key %q on %s — verify with: kay connect --alias %s\n",
			srv.KeyName, srv.Alias, srv.Alias)
		return nil
	}

	fmt.Printf(`To authorise this key on %s@%s, run the following ON THE SERVER:

  mkdir -p ~/.ssh && chmod 700 ~/.ssh && \
  echo '%s' >> ~/.ssh/authorized_keys && \
  chmod 600 ~/.ssh/authorized_keys

Then verify with:  kay connect --alias %s
`, srv.User, srv.Host, pub, srv.Alias)
	return nil
}

// pushKey installs the authorized_keys line on the server over a password
// login, using kay's host-key policy. Shared by `install --push` and the
// console's install screen.
func pushKey(st *config.Store, srv *config.Server, pub, password string) error {
	hostKey, err := hostKeyPolicy(st, false)
	if err != nil {
		return err
	}
	c, err := sshx.Dial(context.Background(), srv.Addr(), sshx.Config{
		User:    srv.User,
		Auth:    []ssh.AuthMethod{ssh.Password(password)},
		HostKey: hostKey,
	})
	if err != nil {
		return dialHint(err, srv, "wrong password?")
	}
	defer c.Close()
	cmd := "mkdir -p ~/.ssh && chmod 700 ~/.ssh && printf '%s\\n' " +
		shellQuote(pub) + " >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"
	out, cerr := c.CombinedOutput(context.Background(), cmd)
	if cerr != nil {
		return fmt.Errorf("install failed: %w: %s", cerr, strings.TrimSpace(out))
	}
	return nil
}

// ---- connect ----

func cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	alias := fs.String("alias", "", "server alias")
	insecure := fs.Bool("insecure", false, "skip host key verification")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := config.Load()
	if err != nil {
		return err
	}
	srv, err := pickServer(st, *alias)
	if err != nil {
		return err
	}
	client, err := dial(st, srv, *insecure)
	if err != nil {
		return err
	}
	defer client.Close()
	fmt.Printf("connected to %s@%s — type 'exit' to leave\n", srv.User, srv.Addr())
	return shell(client)
}

// ---- exec ----

func cmdExec(args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	alias := fs.String("alias", "", "server alias")
	insecure := fs.Bool("insecure", false, "skip host key verification")
	if err := fs.Parse(args); err != nil {
		return err
	}
	remoteCmd := strings.Join(fs.Args(), " ")
	if remoteCmd == "" {
		return fmt.Errorf("no command given (use: kay exec --alias A -- uptime)")
	}
	st, err := config.Load()
	if err != nil {
		return err
	}
	srv, err := pickServer(st, *alias)
	if err != nil {
		return err
	}
	client, err := dial(st, srv, *insecure)
	if err != nil {
		return err
	}
	defer client.Close()
	out, runErr := client.CombinedOutput(context.Background(), remoteCmd)
	fmt.Print(out)
	if !strings.HasSuffix(out, "\n") && out != "" {
		fmt.Println()
	}
	return runErr
}

// ---- dashboard ----

func cmdDashboard(args []string) error {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	alias := fs.String("alias", "", "server alias")
	interval := fs.Duration("interval", 3*time.Second, "refresh interval")
	insecure := fs.Bool("insecure", false, "skip host key verification")
	color := fs.String("color", "auto", "color mode: auto|always|never")
	readonly := fs.Bool("read-only", false, "disable destructive actions (kill/restart/stop)")
	anon := fs.Bool("anonymize", false, "mask host/user/alias + Docker names (for demos/screenshots)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := config.Load()
	if err != nil {
		return err
	}
	srv, err := pickServer(st, *alias)
	if err != nil {
		return err
	}
	client, err := dial(st, srv, *insecure)
	if err != nil {
		return err
	}
	defer client.Close()
	panels, saveLayout := overviewLayoutOpts(st)
	opts := dashboard.Options{
		Interval:  *interval,
		Color:     *color,
		ReadOnly:  *readonly,
		Anonymize: *anon || os.Getenv("KAY_DEMO") != "",
		Redial: func() (dashboard.Client, error) {
			c, rerr := dial(st, srv, *insecure)
			if rerr != nil {
				return nil, rerr
			}
			return runner{c}, nil
		},
		Overview:   panels,
		SaveLayout: saveLayout,
	}
	return dashboardRun(runner{client}, *srv, opts)
}

// overviewLayoutOpts loads the saved Overview layout and returns a saver that
// persists edits back to the store.
func overviewLayoutOpts(st *config.Store) (panels []config.PanelPref, save func([]config.PanelPref) error) {
	return st.OverviewPanels(), func(p []config.PanelPref) error {
		st.SetOverviewPanels(p)
		return st.Save()
	}
}

// ---- fleet ----

func cmdFleet(args []string) error {
	fs := flag.NewFlagSet("fleet", flag.ContinueOnError)
	interval := fs.Duration("interval", 5*time.Second, "refresh interval")
	insecure := fs.Bool("insecure", false, "skip host key verification")
	color := fs.String("color", "auto", "color mode: auto|always|never")
	readonly := fs.Bool("read-only", false, "disable destructive actions when drilling into a host")
	anon := fs.Bool("anonymize", false, "mask aliases/hosts (for demos/screenshots)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := config.Load()
	if err != nil {
		return err
	}
	hostKey, err := hostKeyPolicy(st, *insecure)
	if err != nil {
		return err
	}
	hosts := fleetHosts(st, hostKey)
	fopts := fleet.Options{
		Interval:  *interval,
		Color:     *color,
		Anonymize: *anon || os.Getenv("KAY_DEMO") != "",
	}
	// Non-interactive: plain, no drill-in.
	if !isTerminal(int(os.Stdin.Fd())) {
		return fleetRun(hosts, fopts)
	}
	return fleetDrill(st, hosts, fopts, *readonly)
}

// inputRouter owns process stdin for an interactive session: a single reader
// goroutine feeds chunks that are either decoded into UI key events or, while
// the console lends the terminal to an SSH shell, diverted raw to it — never
// both, so the shell and the UI can't fight over keystrokes.
type inputRouter struct {
	events chan tui.Event
	divert chan io.Writer
	chunks chan []byte
}

func startInputRouter() *inputRouter {
	r := &inputRouter{events: make(chan tui.Event, 16), divert: make(chan io.Writer), chunks: make(chan []byte, 16)}
	in := stdinFile // captured once: the test seam may be restored while we run
	go func() {     // the sole stdin reader
		for {
			buf := make([]byte, 1024)
			n, err := in.Read(buf)
			if n > 0 {
				r.chunks <- buf[:n]
			}
			if err != nil {
				close(r.chunks)
				return
			}
		}
	}()
	go r.route()
	return r
}

// route decodes chunks into events, or copies them raw while diverted. Event
// delivery stays receptive to divert requests so a busy UI can never
// deadlock the handoff, and every mode switch drops what was buffered for
// the other side — keys typed ahead of a shell must not be injected into it,
// and keys typed at a shell must not replay into the UI. Stdin ending
// delivers a final quit event, like a reader error always has.
func (r *inputRouter) route() {
	var raw io.Writer
	var pending []byte
	for {
		select {
		case w := <-r.divert:
			if w != nil {
				r.drainChunks()
			}
			pending, raw = nil, w
		case chunk, ok := <-r.chunks:
			if !ok {
				r.events <- tui.Event{Type: tui.EventQuit}
				return
			}
			if raw != nil {
				_, _ = raw.Write(chunk)
				continue
			}
			pending = append(pending, chunk...)
			for {
				ev, n := tui.Decode(pending)
				if n == 0 {
					break
				}
				pending = pending[n:]
				select {
				case r.events <- ev:
				case w := <-r.divert:
					if w != nil {
						r.drainChunks()
					}
					pending, raw = nil, w
				}
				if raw != nil {
					break
				}
			}
		}
	}
}

// drainChunks drops chunks buffered for the UI when switching to a shell:
// keystrokes typed ahead of the handoff must not be injected into it. The
// reverse drop (stale events on the way back) runs caller-side in divertTo —
// the UI goroutine owns event consumption, so only there is it race-free.
func (r *inputRouter) drainChunks() {
	for {
		select {
		case _, ok := <-r.chunks:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

// divertTo routes raw stdin to w; nil returns input to the UI decoder,
// dropping events decoded before or during the handoff so keys typed at the
// shell can never replay into the UI.
func (r *inputRouter) divertTo(w io.Writer) {
	r.divert <- w
	if w != nil {
		return
	}
	for {
		select {
		case <-r.events:
		default:
			return
		}
	}
}

// fleetHosts wraps every stored server with a dial closure bound to the shared
// host-key policy, ready for a fleet session.
func fleetHosts(st *config.Store, hostKey ssh.HostKeyCallback) []fleet.Host {
	hosts := make([]fleet.Host, 0, len(st.Servers))
	for i := range st.Servers {
		srv := st.Servers[i] // copy so each closure binds its own server
		hosts = append(hosts, fleet.Host{
			Server: srv,
			Dial: func(ctx context.Context) (*sshx.Client, error) {
				return dialWith(ctx, st, &srv, hostKey)
			},
		})
	}
	return hosts
}

// fleetDrill runs the interactive fleet overview with drill-in: it owns a single
// screen, input reader, and connection pool for the whole session, so pressing
// Enter on a host hands the terminal to that host's dashboard and back with no
// flicker, no competing stdin readers, and no second SSH handshake — the
// dashboard reuses the connection the fleet already established.
func fleetDrill(st *config.Store, hosts []fleet.Host, fopts fleet.Options, readOnly bool) error {
	tui.SetColorMode(fopts.Color)
	scr, err := newScreen()
	if err != nil {
		return err
	}
	defer scr.Close()

	sess := fleet.NewSession(hosts)
	defer sess.Close()

	events := startInputRouter().events

	for {
		sel, err := sess.RunView(scr, events, fopts)
		if err != nil {
			return err
		}
		if sel == nil {
			return nil // user quit the fleet
		}
		srv := sel.Host.Server
		panels, saveLayout := overviewLayoutOpts(st)
		dopts := dashboard.Options{
			Interval:   fopts.Interval,
			Color:      fopts.Color,
			ReadOnly:   readOnly,
			Anonymize:  fopts.Anonymize,
			Overview:   panels,
			SaveLayout: saveLayout,
			// No Redial: the reused connection is pool-managed and self-heals, so
			// the dashboard just retries its metrics over the same seam.
		}
		// Reuse the pooled connection; the pool owns it, so we must NOT close it here.
		exitApp, derr := dashboard.RunView(scr, events, sel.Client, srv, dopts)
		if derr != nil {
			return derr
		}
		if exitApp {
			return nil // Ctrl-C / SIGTERM inside the dashboard exits the whole app
		}
		// q / Esc in the dashboard: loop back to the fleet overview.
	}
}

// ---- ls (overview) ----

func cmdLs(_ []string) error {
	st, err := config.Load()
	if err != nil {
		return err
	}
	anon := anonEnabled()
	dir := st.Dir()
	if anon {
		dir = "<config dir>"
	}
	fmt.Printf("config: %s\n\nKEYS (%d):\n", dir, len(st.Keys))
	for i, k := range st.Keys {
		name, fp := k.Name, k.Fingerprint
		if anon {
			name, fp = fmt.Sprintf("key-%d", i+1), "SHA256:…"
		}
		fmt.Printf("  %-12s %-8s %s\n", name, k.Type, fp)
	}
	fmt.Printf("\nSERVERS (%d):\n", len(st.Servers))
	for i, s := range st.Servers {
		alias, user, host, keyn := s.Alias, s.User, s.Host, s.KeyName
		if anon {
			alias, user, host, keyn = fmt.Sprintf("server-%d", i+1), "user", "demo.host", "key"
		}
		fmt.Printf("  %-12s %s@%s:%d  key=%s\n", alias, user, host, s.Port, keyn)
	}
	return nil
}

// ---- shared helpers ----

// anonEnabled reports whether demo redaction is on (KAY_DEMO), used by the
// listing commands to mask hosts, users, aliases, key names, and fingerprints.
func anonEnabled() bool { return os.Getenv("KAY_DEMO") != "" }

// shellQuote wraps a string in single quotes for safe use in a remote shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pickServer returns the named server, or prompts interactively when no alias
// was provided (satisfies "user can choose it in the CLI").
func pickServer(st *config.Store, alias string) (*config.Server, error) {
	if alias != "" {
		return st.FindServer(alias)
	}
	if len(st.Servers) == 0 {
		return nil, fmt.Errorf("no servers registered; add one with 'kay server add'")
	}
	if len(st.Servers) == 1 {
		return &st.Servers[0], nil
	}
	fmt.Fprintln(os.Stderr, "Select a server:")
	for i, s := range st.Servers {
		fmt.Fprintf(os.Stderr, "  [%d] %s (%s@%s)\n", i+1, s.Alias, s.User, s.Host)
	}
	fmt.Fprint(os.Stderr, "> ")
	reader := bufio.NewReader(stdinReader)
	text, _ := reader.ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 1 || n > len(st.Servers) {
		return nil, fmt.Errorf("invalid selection")
	}
	return &st.Servers[n-1], nil
}
