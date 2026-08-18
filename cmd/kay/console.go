package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wigata-Intech/kay/internal/app"
	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/fleet"
	"github.com/Wigata-Intech/kay/internal/keys"
	"github.com/Wigata-Intech/kay/internal/tui"

	sshx "github.com/Wigata-Intech/w-tools/x/sshx"
	"golang.org/x/crypto/ssh"
)

// runConsole opens the interactive console — where bare `kay` on a TTY lands.
// It owns one screen, one input reader, and one connection pool for the whole
// session and hands them to app.Console: the fleet overview as home, Enter
// drilling into a host's dashboard over the pooled connection, and the
// management screens on the console keys.
func runConsole() error {
	st, err := config.Load()
	if err != nil {
		return err
	}
	hostKey, err := hostKeyPolicy(st, false)
	if err != nil {
		return err
	}

	// Same interval as the `kay fleet` default, so a dashboard drilled into
	// from either entry refreshes at the same cadence.
	c := app.NewConsole(st,
		func() []fleet.Host { return fleetHosts(st, hostKey) },
		fleet.Options{Interval: 5 * time.Second, Anonymize: anonEnabled()},
		false)

	tui.SetColorMode("auto")
	scr, err := newScreen()
	if err != nil {
		return err
	}
	defer scr.Close()

	// Dials must not start before this swap, and every console handed out
	// here must reach Run (its exit releases blocked prompts).
	confirmHostFn = consoleConfirmHost(c)
	loadSigner = func(path string) (ssh.Signer, error) {
		return keys.LoadSignerWith(path, consolePassphrase(c))
	}

	router := startInputRouter()
	c.Connect = consoleConnect(st, hostKey, router)
	c.InstallKey = consoleInstallKey(st)

	return c.Run(scr, router.events)
}

// runShell hands the terminal to an interactive shell whose input is the
// diverted router stream; a seam so the grand-tour test can run headless.
var runShell = func(client *sshx.Client, stdin io.Reader) error {
	return shellWith(client, realTerm{}, stdinFile, stdin, os.Stdout, os.Stderr)
}

// consoleConnect dials the server (off the UI goroutine — the console pumps
// prompts meanwhile) and returns the shell handoff: raw stdin diverted from
// the input router into the session. The pipe is closed before un-diverting
// so a raw write can never block the handoff back.
func consoleConnect(st *config.Store, hostKey ssh.HostKeyCallback, router *inputRouter) func(config.Server) (func() error, func(), error) {
	return func(srv config.Server) (func() error, func(), error) {
		client, err := dialWith(context.Background(), st, &srv, hostKey)
		if err != nil {
			return nil, nil, err
		}
		shell := func() error {
			pr, pw := io.Pipe()
			router.divertTo(pw)
			defer func() {
				_ = pw.Close()
				router.divertTo(nil)
			}()
			return runShell(client, pr)
		}
		return shell, func() { _ = client.Close() }, nil
	}
}

// consoleInstallKey pushes the server's public key over a password login —
// the console install screen's callback.
func consoleInstallKey(st *config.Store) func(srv config.Server, password string) error {
	return func(srv config.Server, password string) error {
		k, err := st.FindKey(srv.KeyName)
		if err != nil {
			return err
		}
		pub, err := keys.ReadPublic(k.PublicPath)
		if err != nil {
			return err
		}
		return pushKey(st, &srv, strings.TrimSpace(pub), password)
	}
}

// consoleConfirmHost surfaces the TOFU decision as a console modal instead of
// the terminal prompt. A decline is remembered for the rest of the session:
// the pool redials failed hosts on backoff forever, and without the memory
// every retry would raise the same modal again.
func consoleConfirmHost(c *app.Console) sshx.ConfirmHostFunc {
	var mu sync.Mutex
	declined := make(map[string]bool) // host|fingerprint
	return func(h sshx.HostInfo) (bool, error) {
		id := h.Host + "|" + h.Fingerprint
		mu.Lock()
		seen := declined[id]
		mu.Unlock()
		if seen {
			return false, nil
		}
		ok := c.AskYesNo("Unknown host", []string{
			fmt.Sprintf("The authenticity of host %s can't be established.", h.Host),
			fmt.Sprintf("%s key fingerprint is %s", h.KeyType, h.Fingerprint),
			"Trust this host and continue connecting?",
		})
		if !ok {
			mu.Lock()
			declined[id] = true
			mu.Unlock()
		}
		return ok, nil
	}
}

// consolePassphrase surfaces an encrypted key's passphrase prompt as a masked
// console modal. A cancel is remembered like a host decline — a wrong
// passphrase is not, so honest retries still prompt.
func consolePassphrase(c *app.Console) keys.PassphraseFunc {
	var mu sync.Mutex
	canceled := make(map[string]bool)
	return func(name string) ([]byte, error) {
		mu.Lock()
		seen := canceled[name]
		mu.Unlock()
		if seen {
			return nil, fmt.Errorf("passphrase entry canceled for key %q", name)
		}
		secret, ok := c.AskSecret("Encrypted key", fmt.Sprintf("Passphrase for %q", name))
		if !ok {
			mu.Lock()
			canceled[name] = true
			mu.Unlock()
			return nil, fmt.Errorf("passphrase entry canceled for key %q", name)
		}
		return []byte(secret), nil
	}
}
