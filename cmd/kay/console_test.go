// White-box (package main): drives runConsole and the bare-`kay` dispatch
// through the screen/stdin/terminal seams.
package main

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/app"
	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/fleet"
	"github.com/Wigata-Intech/kay/internal/tui"

	sshx "github.com/Wigata-Intech/w-tools/x/sshx"
)

// consoleFixture persists a one-host store (runConsole re-loads it from disk),
// pins the test server's host key, and swaps the screen seam for a fake.
func consoleFixture(t *testing.T) (*fakeScreen, *testSSHServer) {
	t.Helper()
	st, pub := newKeyedStore(t)
	// Screen seam first: its cleanup (restoring the prompt seams) then runs
	// after the server's, which waits out the session's connections.
	scr := fakeConsoleScreen(t)
	server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
	addServer(t, st, "test", server.addr())
	pinHostKey(t, st, server)
	saveStore(t, st)
	return scr, server
}

// fakeConsoleScreen swaps the newScreen seam for a recording fake and
// restores the prompt seams runConsole repoints at its console.
func fakeConsoleScreen(t *testing.T) *fakeScreen {
	t.Helper()
	scr := &fakeScreen{}
	prev := newScreen
	prevConfirm, prevLoad := confirmHostFn, loadSigner
	newScreen = func() (uiScreen, error) { return scr, nil }
	t.Cleanup(func() { newScreen, confirmHostFn, loadSigner = prev, prevConfirm, prevLoad })
	return scr
}

func TestRunConsole(t *testing.T) {
	t.Run("quit at the home view", func(t *testing.T) {
		scr, _ := consoleFixture(t)
		scriptStdin(t, []stdinStep{{delay: 50 * time.Millisecond, keys: "q"}})
		if err := runConsole(); err != nil {
			t.Errorf("runConsole() error = %v", err)
		}
		if scr.dashboardSeen() {
			t.Error("dashboard drawn without a drill-in")
		}
	})

	t.Run("drill into the dashboard and back", func(t *testing.T) {
		scr, server := consoleFixture(t)
		w := scriptStdin(t, nil)
		go drillKeys(w, server, "q", "q") // back to the home view, then quit
		if err := runConsole(); err != nil {
			t.Errorf("runConsole() error = %v", err)
		}
		if !scr.dashboardSeen() {
			t.Error("dashboard never drawn: the drill-in did not happen")
		}
	})

	t.Run("empty store shows the welcome view", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		scr := fakeConsoleScreen(t)
		scriptStdin(t, []stdinStep{{delay: 50 * time.Millisecond, keys: "q"}})
		if err := runConsole(); err != nil {
			t.Errorf("runConsole() error = %v", err)
		}
		if !scr.saw("No servers registered") {
			t.Error("welcome view never drawn for an empty store")
		}
	})

	t.Run("store load failure is returned", func(t *testing.T) {
		noConfigDir(t)
		if err := runConsole(); err == nil {
			t.Error("runConsole() error = nil, want store load failure")
		}
	})

	t.Run("unusable known_hosts fails the policy", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("KAY_HOME", home)
		if _, err := config.Load(); err != nil {
			t.Fatalf("load: %v", err)
		}
		lockDir(t, home)
		if err := runConsole(); err == nil {
			t.Error("runConsole() error = nil, want host-key policy failure")
		}
	})

	t.Run("first contact raises the TOFU modal; y pins the host", func(t *testing.T) {
		st, pub := newKeyedStore(t)
		scr := fakeConsoleScreen(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
		addServer(t, st, "test", server.addr())
		saveStore(t, st) // deliberately NOT pinned: the dial must ask

		w := scriptStdin(t, nil)
		go func() {
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) && !scr.saw("Unknown host") {
				time.Sleep(5 * time.Millisecond)
			}
			_, _ = w.Write([]byte("y"))
			for time.Now().Before(deadline) {
				if data, err := os.ReadFile(st.KnownHostsPath()); err == nil && len(data) > 0 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			_, _ = w.Write([]byte("q"))
		}()
		if err := runConsole(); err != nil {
			t.Fatalf("runConsole() error = %v", err)
		}
		if !scr.saw("Unknown host") {
			t.Error("TOFU modal never drawn")
		}
		data, err := os.ReadFile(st.KnownHostsPath())
		if err != nil || !strings.Contains(string(data), "ssh-") {
			t.Errorf("known_hosts after trust = %q, %v; want the pinned key", data, err)
		}
	})

	t.Run("keys are generated end to end from the welcome view", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		scr := fakeConsoleScreen(t)
		// welcome -> K (keys) -> n (generate) -> name -> submit through the
		// prefilled type/bits -> back out and quit.
		scriptStdin(t, []stdinStep{{delay: 50 * time.Millisecond,
			keys: "Knmykey\r\r\r\x1b\x1b" + "q"}})
		if err := runConsole(); err != nil {
			t.Fatalf("runConsole() error = %v", err)
		}
		st, err := config.Load()
		if err != nil {
			t.Fatalf("reload store: %v", err)
		}
		k, err := st.FindKey("mykey")
		if err != nil {
			t.Fatalf("generated key not stored: %v", err)
		}
		if _, err := os.Stat(k.PrivatePath); err != nil {
			t.Errorf("private key file missing: %v", err)
		}
		if !scr.saw("Keys — 1") {
			t.Error("keys view never showed the generated key")
		}
	})

	t.Run("screen failure is returned", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		wantErr := errors.New("no tty")
		prev := newScreen
		newScreen = func() (uiScreen, error) { return nil, wantErr }
		t.Cleanup(func() { newScreen = prev })
		if err := runConsole(); !errors.Is(err, wantErr) {
			t.Errorf("runConsole() error = %v, want %v", err, wantErr)
		}
	})
}

func TestConsolePassphrase(t *testing.T) {
	t.Run("round-trips the typed passphrase", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		st, err := config.Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		c := app.NewConsole(st, func() []fleet.Host { return nil }, fleet.Options{}, false)
		scr := &fakeScreen{}
		events := make(chan tui.Event, 16)
		runDone := make(chan error, 1)
		go func() { runDone <- c.Run(scr, events) }()

		type answer struct {
			pass []byte
			err  error
		}
		got := make(chan answer, 1)
		go func() {
			pass, perr := consolePassphrase(c)("id")
			got <- answer{pass, perr}
		}()

		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && !scr.saw("Encrypted key") {
			time.Sleep(5 * time.Millisecond)
		}
		for _, r := range "pw" {
			events <- tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: r}
		}
		events <- tui.Event{Type: tui.EventKey, Key: tui.KeyEnter}
		events <- tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'q'}

		select {
		case a := <-got:
			if a.err != nil || string(a.pass) != "pw" {
				t.Errorf("passphrase = (%q, %v), want (pw, nil)", a.pass, a.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("passphrase prompt never answered")
		}
		if err := <-runDone; err != nil {
			t.Errorf("Run() error = %v", err)
		}
	})

	t.Run("cancel and console exit fail closed", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		st, err := config.Load()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		c := app.NewConsole(st, func() []fleet.Host { return nil }, fleet.Options{}, false)
		if err := c.Run(&fakeScreen{}, send(tui.Event{Type: tui.EventQuit})); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if _, perr := consolePassphrase(c)("id"); perr == nil {
			t.Error("passphrase after console exit = nil error, want canceled")
		}
	})
}

// send builds a buffered event channel preloaded with evs.
func send(evs ...tui.Event) chan tui.Event {
	ch := make(chan tui.Event, len(evs))
	for _, ev := range evs {
		ch <- ev
	}
	return ch
}

// startAppConsole runs a bare app console (no servers, welcome view) that
// answers prompts from the scripted events; quits on EventQuit.
func startAppConsole(t *testing.T) (*app.Console, *fakeScreen, chan tui.Event) {
	t.Helper()
	t.Setenv("KAY_HOME", t.TempDir())
	st, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	c := app.NewConsole(st, func() []fleet.Host { return nil }, fleet.Options{}, false)
	scr := &fakeScreen{}
	events := make(chan tui.Event, 16)
	runDone := make(chan error, 1)
	go func() { runDone <- c.Run(scr, events) }()
	t.Cleanup(func() {
		events <- tui.Event{Type: tui.EventQuit}
		if err := <-runDone; err != nil {
			t.Errorf("Run() error = %v", err)
		}
	})
	return c, scr, events
}

// waitSaw polls until the screen drew a line containing s.
func waitSaw(t *testing.T, scr *fakeScreen, s string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !scr.saw(s) {
		time.Sleep(5 * time.Millisecond)
	}
	if !scr.saw(s) {
		t.Fatalf("%q never drawn", s)
	}
}

func TestConsoleConfirmHostDeclineMemory(t *testing.T) {
	c, scr, events := startAppConsole(t)
	confirm := consoleConfirmHost(c)
	info := sshx.HostInfo{Host: "10.0.0.1:22", KeyType: "ssh-ed25519", Fingerprint: "SHA256:abc"}

	got := make(chan bool, 1)
	go func() { ok, _ := confirm(info); got <- ok }()
	waitSaw(t, scr, "Unknown host")
	events <- tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'n'}
	if ok := <-got; ok {
		t.Fatal("declined confirm returned true")
	}

	// The redial's repeat question must be answered from memory, without a
	// modal — synchronously, with nobody feeding events.
	if ok, err := confirm(info); ok || err != nil {
		t.Errorf("repeat confirm = (%v, %v), want remembered decline", ok, err)
	}

	// A different host still asks.
	go func() { ok, _ := confirm(sshx.HostInfo{Host: "10.0.0.2:22", Fingerprint: "SHA256:other"}); got <- ok }()
	waitSaw(t, scr, "10.0.0.2:22")
	events <- tui.Event{Type: tui.EventKey, Key: tui.KeyRune, Rune: 'y'}
	if ok := <-got; !ok {
		t.Error("fresh host confirm returned false after y")
	}
}

func TestConsolePassphraseCancelMemory(t *testing.T) {
	c, scr, events := startAppConsole(t)
	prompt := consolePassphrase(c)

	errCh := make(chan error, 1)
	go func() { _, err := prompt("id"); errCh <- err }()
	waitSaw(t, scr, "Passphrase")
	events <- tui.Event{Type: tui.EventKey, Key: tui.KeyEsc}
	if err := <-errCh; err == nil {
		t.Fatal("canceled prompt returned nil error")
	}

	// The retrying dial must fail from memory without a fresh modal.
	if _, err := prompt("id"); err == nil {
		t.Error("repeat prompt after cancel = nil error, want remembered cancel")
	}
}

// tourDriver paces scripted keystrokes on observable console state, from a
// goroutine; failures are recorded (not fatal — that must happen on the test
// goroutine) and a Ctrl-C forces the console down so the test can report.
type tourDriver struct {
	w    *os.File
	mu   sync.Mutex
	fail string
}

func (d *tourDriver) step(name string, cond func() bool, keys string) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !cond() {
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		d.mu.Lock()
		d.fail = name
		d.mu.Unlock()
		_, _ = d.w.Write([]byte{0x03}) // force the console down
		return false
	}
	_, _ = d.w.Write([]byte(keys))
	return true
}

func (d *tourDriver) failure() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fail
}

// TestConsoleGrandTour walks the whole onboarding journey headless: generate
// a key, register the server, trust its host key through the TOFU modal, and
// install the key over a password login — all inside one console session.
func TestConsoleGrandTour(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	server := newTestSSHServer(t, testServerOpts{password: "pw"})
	scr := fakeConsoleScreen(t)
	w := scriptStdin(t, nil)

	host, portStr, err := net.SplitHostPort(server.addr())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}

	st, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	d := &tourDriver{w: w}
	go func() {
		_ = d.step("welcome", func() bool { return scr.saw("No servers registered") }, "K") &&
			d.step("keys view", func() bool { return scr.saw("No keys yet") }, "n") &&
			d.step("key form", func() bool { return scr.saw("Generate key") }, "mykey\r\r\r") &&
			d.step("key listed", func() bool { return scr.saw("Keys — 1") }, "\x1b") &&
			d.step("welcome again", func() bool { return true }, "a") &&
			d.step("server form", func() bool { return scr.saw("Add server") },
				"web\r"+host+"\r\x7f\x7f"+portStr+"\rtester\r\r") &&
			d.step("tofu modal", func() bool { return scr.saw("Unknown host") }, "y") &&
			d.step("host pinned", func() bool {
				data, rerr := os.ReadFile(st.KnownHostsPath())
				return rerr == nil && len(data) > 0
			}, "i") &&
			d.step("install view", func() bool { return scr.saw("install @ web") }, "p") &&
			d.step("password prompt", func() bool { return scr.saw("Password for tester@") }, "pw\r") &&
			d.step("install result", func() bool { return scr.saw("Key installed") }, "\r\x1bq")
	}()

	if err := runConsole(); err != nil {
		t.Fatalf("runConsole() error = %v", err)
	}
	if f := d.failure(); f != "" {
		t.Fatalf("tour stalled at step %q", f)
	}

	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	if _, err := reloaded.FindKey("mykey"); err != nil {
		t.Errorf("generated key missing: %v", err)
	}
	if srv, err := reloaded.FindServer("web"); err != nil || srv.Host != host {
		t.Errorf("registered server = %+v, %v; want host %s", srv, err, host)
	}
	installed := false
	for _, cmd := range server.execRecords() {
		if strings.Contains(cmd, "authorized_keys") {
			installed = true
		}
	}
	if !installed {
		t.Error("install command never ran on the server")
	}
}

// TestConsoleExecConnectTour drives the working-host half of the journey:
// dashboard drill-in, a remote command through the exec view, and a connect
// handoff — over one pooled session.
func TestConsoleExecConnectTour(t *testing.T) {
	scr, server := consoleFixture(t)
	w := scriptStdin(t, nil)

	var shellMu sync.Mutex
	shells := 0
	prevShell := runShell
	runShell = func(client *sshx.Client, stdin io.Reader) error {
		shellMu.Lock()
		shells++
		shellMu.Unlock()
		return nil
	}
	t.Cleanup(func() { runShell = prevShell })
	shellCount := func() int { shellMu.Lock(); defer shellMu.Unlock(); return shells }

	d := &tourDriver{w: w}
	go func() {
		_ = d.step("host ready", func() bool { return len(server.execRecords()) > 0 }, "\r") &&
			d.step("dashboard", func() bool { return scr.dashboardSeen() }, "q") &&
			d.step("back at the fleet", func() bool { return true }, "x") &&
			d.step("exec view", func() bool { return scr.saw("exec @ test") }, "uptime\r") &&
			d.step("command ran", func() bool {
				for _, cmd := range server.execRecords() {
					if cmd == "uptime" {
						return true
					}
				}
				return false
			}, "\x1b") &&
			d.step("connect", func() bool { return true }, "c") &&
			d.step("shell returned", func() bool { return shellCount() == 1 }, "q")
	}()

	if err := runConsole(); err != nil {
		t.Fatalf("runConsole() error = %v", err)
	}
	if f := d.failure(); f != "" {
		t.Fatalf("tour stalled at step %q", f)
	}
}

func TestConsoleConnectDialError(t *testing.T) {
	st, _ := newKeyedStore(t)
	srv := addServer(t, st, "web", "127.0.0.1:1") // nothing listens there
	hostKey, err := hostKeyPolicy(st, true)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	scriptStdin(t, nil) // the router must not read the real stdin
	connect := consoleConnect(st, hostKey, startInputRouter())
	if _, _, err := connect(*srv); err == nil {
		t.Error("connect to a dead address = nil error")
	}
}

func TestRunShellDefault(t *testing.T) {
	// The default seam body: a real session over the rig, no TTY (stdin is a
	// test pipe), shell exits immediately.
	st, pub := newKeyedStore(t)
	server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
	srv := addServer(t, st, "test", server.addr())
	c, err := dialWith(t.Context(), st, srv, server.hostKey())
	if err != nil {
		t.Fatalf("dialWith() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := runShell(c, strings.NewReader("")); err != nil {
		t.Errorf("runShell() error = %v", err)
	}
}

func TestConsoleInstallKeyErrors(t *testing.T) {
	st, _ := newKeyedStore(t)
	install := consoleInstallKey(st)

	t.Run("unknown key", func(t *testing.T) {
		if err := install(config.Server{Alias: "web", KeyName: "ghost"}, "pw"); err == nil {
			t.Error("install with unknown key = nil error")
		}
	})

	t.Run("unreadable public key", func(t *testing.T) {
		k, err := st.FindKey("id")
		if err != nil {
			t.Fatalf("find key: %v", err)
		}
		if err := os.Remove(k.PublicPath); err != nil {
			t.Fatalf("remove pub: %v", err)
		}
		if err := install(config.Server{Alias: "web", KeyName: "id"}, "pw"); err == nil {
			t.Error("install with unreadable key = nil error")
		}
	})
}

func TestMainConsole(t *testing.T) {
	t.Run("bare kay on a terminal opens the console", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		setIsTerminal(t, true)
		scr := fakeConsoleScreen(t)
		scriptStdin(t, []stdinStep{{delay: 50 * time.Millisecond, keys: "q"}})
		code, exited := runMain(t)
		if exited || code != 0 {
			t.Errorf("main() = code %d, exited %v; want a clean console session", code, exited)
		}
		if !scr.saw("No servers registered") {
			t.Error("console never drawn")
		}
	})

	t.Run("console failure exits 1", func(t *testing.T) {
		noConfigDir(t)
		setIsTerminal(t, true)
		code, exited := runMain(t)
		if !exited || code != 1 {
			t.Errorf("main() = code %d, exited %v; want exit 1", code, exited)
		}
	})
}
