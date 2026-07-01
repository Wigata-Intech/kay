// Command kay is a small CLI for managing a fleet of Linux servers over SSH:
// generate keys, register servers, install keys, run commands, and watch a
// refreshing metrics dashboard.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/dashboard"
	"github.com/Wigata-Intech/kay/internal/fleet"
	"github.com/Wigata-Intech/kay/internal/keys"
	"github.com/Wigata-Intech/kay/internal/sshx"

	"golang.org/x/term"
)

// Set at build time via -ldflags (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = ""
	date    = ""
)

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
		usage()
		os.Exit(2)
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
		os.Exit(1)
	}
	if err := h(args); err != nil {
		fmt.Fprintln(os.Stderr, "kay: "+err.Error())
		os.Exit(1)
	}
}

// cmdVersion prints the build version with optional commit/date, set via ldflags.
func cmdVersion() {
	v := version
	var extra []string
	if commit != "" {
		extra = append(extra, commit)
	}
	if date != "" {
		extra = append(extra, date)
	}
	if len(extra) > 0 {
		v += " (" + strings.Join(extra, ", ") + ")"
	}
	fmt.Println("kay " + v)
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
		pw, rerr := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if rerr != nil {
			return rerr
		}
		c, derr := sshx.Dial(sshx.DialOptions{
			Addr: srv.Addr(), User: srv.User, Password: string(pw),
			KnownHostsPath: st.KnownHostsPath(),
		})
		if derr != nil {
			return derr
		}
		defer c.Close()
		cmd := "mkdir -p ~/.ssh && chmod 700 ~/.ssh && printf '%s\\n' " +
			shellQuote(pub) + " >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys"
		out, cerr := c.Run(cmd)
		if cerr != nil {
			return fmt.Errorf("install failed: %w: %s", cerr, strings.TrimSpace(out))
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
	return client.Shell()
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
	out, runErr := client.Run(remoteCmd)
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
	opts := dashboard.Options{
		Interval:  *interval,
		Color:     *color,
		ReadOnly:  *readonly,
		Anonymize: *anon || os.Getenv("KAY_DEMO") != "",
		Redial:    func() (dashboard.Client, error) { return dial(st, srv, *insecure) },
	}
	return dashboard.Run(client, *srv, opts)
}

// ---- fleet ----

func cmdFleet(args []string) error {
	fs := flag.NewFlagSet("fleet", flag.ContinueOnError)
	interval := fs.Duration("interval", 5*time.Second, "refresh interval")
	insecure := fs.Bool("insecure", false, "skip host key verification")
	color := fs.String("color", "auto", "color mode: auto|always|never")
	anon := fs.Bool("anonymize", false, "mask aliases/hosts (for demos/screenshots)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := config.Load()
	if err != nil {
		return err
	}
	hosts := make([]fleet.Host, 0, len(st.Servers))
	for i := range st.Servers {
		srv := st.Servers[i] // copy so each closure binds its own server
		hosts = append(hosts, fleet.Host{
			Server: srv,
			Dial:   func() (*sshx.Client, error) { return dial(st, &srv, *insecure) },
		})
	}
	return fleet.Run(hosts, fleet.Options{
		Interval:  *interval,
		Color:     *color,
		Anonymize: *anon || os.Getenv("KAY_DEMO") != "",
	})
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

// dial loads the server's key and opens a connection.
func dial(st *config.Store, srv *config.Server, insecure bool) (*sshx.Client, error) {
	k, err := st.FindKey(srv.KeyName)
	if err != nil {
		return nil, err
	}
	signer, err := keys.LoadSigner(k.PrivatePath)
	if err != nil {
		return nil, err
	}
	return sshx.Dial(sshx.DialOptions{
		Addr:           srv.Addr(),
		User:           srv.User,
		Signer:         signer,
		KnownHostsPath: st.KnownHostsPath(),
		Insecure:       insecure,
	})
}

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
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 1 || n > len(st.Servers) {
		return nil, fmt.Errorf("invalid selection")
	}
	return &st.Servers[n-1], nil
}
