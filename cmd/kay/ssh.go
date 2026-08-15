package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/keys"

	sshx "github.com/Wigata-Intech/w-tools/x/sshx"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// dial loads the server's key and opens a connection with kay's host-key
// policy (TOFU against kay's known_hosts, or none with --insecure).
func dial(st *config.Store, srv *config.Server, insecure bool) (*sshx.Client, error) {
	hostKey, err := hostKeyPolicy(st, insecure)
	if err != nil {
		return nil, err
	}
	return dialWith(context.Background(), st, srv, hostKey)
}

// dialWith opens a connection using an already-built host-key policy, so
// callers dialing many hosts (fleet) share one policy and its TOFU state.
func dialWith(ctx context.Context, st *config.Store, srv *config.Server, hostKey ssh.HostKeyCallback) (*sshx.Client, error) {
	k, err := st.FindKey(srv.KeyName)
	if err != nil {
		return nil, err
	}
	signer, err := keys.LoadSigner(k.PrivatePath)
	if err != nil {
		return nil, err
	}
	c, err := sshx.Dial(ctx, srv.Addr(), sshx.Config{
		User:    srv.User,
		Auth:    []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKey: hostKey,
	})
	if err != nil {
		return nil, dialHint(err, srv, "is the public key in the server's authorized_keys?")
	}
	return c, nil
}

// hostKeyPolicy returns kay's host-key verification: trust-on-first-use with a
// terminal confirmation, pinned to kay's own known_hosts file — or none when
// the user passed --insecure.
func hostKeyPolicy(st *config.Store, insecure bool) (ssh.HostKeyCallback, error) {
	if insecure {
		return sshx.InsecureAcceptAny(), nil
	}
	return sshx.TOFU(st.KnownHostsPath(), confirmHostTTY)
}

// dialHint turns sshx's staged dial errors into actionable kay messages,
// appending authHint when the handshake stage looks like an auth failure.
func dialHint(err error, srv *config.Server, authHint string) error {
	var de *sshx.DialError
	if !errors.As(err, &de) {
		return err
	}
	switch de.Stage {
	case sshx.StageHostKey:
		return fmt.Errorf("host key check failed for %s: %w (use --insecure to override)", srv.Addr(), de.Err)
	case sshx.StageHandshake:
		if sshx.IsAuthFailure(de.Err) {
			return fmt.Errorf("authentication failed for %s@%s: %w — %s", srv.User, srv.Addr(), de.Err, authHint)
		}
		return fmt.Errorf("cannot connect to %s: %w", srv.Addr(), de.Err)
	default:
		return fmt.Errorf("cannot connect to %s: %w", srv.Addr(), de.Err)
	}
}

// confirmHostTTY asks for consent to trust a previously-unseen host on the
// real terminal, refusing when there is no TTY to ask.
func confirmHostTTY(h sshx.HostInfo) (bool, error) {
	return confirmHost(os.Stdin, os.Stderr, term.IsTerminal(int(os.Stdin.Fd())), h)
}

// confirmHost is the terminal-independent core of the TOFU prompt, split out
// so the accept, reject, and no-terminal paths are testable without a TTY.
func confirmHost(in io.Reader, out io.Writer, isTTY bool, h sshx.HostInfo) (bool, error) {
	if !isTTY {
		fmt.Fprintf(out, "kay: unknown host %s and no terminal to confirm — refusing.\n", h.Host)
		return false, nil
	}
	fmt.Fprintf(out, "The authenticity of host %s can't be established.\n", h.Host)
	fmt.Fprintf(out, "%s key fingerprint is %s\n", h.KeyType, h.Fingerprint)
	fmt.Fprint(out, "Trust this host and continue connecting? (yes/no): ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "yes" || line == "y", nil
}

// runner adapts a client to the Run seam the dashboard and metrics use.
type runner struct{ c *sshx.Client }

func (r runner) Run(cmd string) (string, error) {
	return r.c.CombinedOutput(context.Background(), cmd)
}

// termIO is the local-terminal capability shell needs, injected so the
// raw-mode and resize paths are testable without a real TTY.
type termIO interface {
	IsTerminal(fd int) bool
	MakeRaw(fd int) (*term.State, error)
	Restore(fd int, state *term.State) error
	GetSize(fd int) (w, h int, err error)
}

// realTerm implements termIO with the process's actual terminal.
type realTerm struct{}

func (realTerm) IsTerminal(fd int) bool               { return term.IsTerminal(fd) }
func (realTerm) MakeRaw(fd int) (*term.State, error)  { return term.MakeRaw(fd) }
func (realTerm) Restore(fd int, s *term.State) error  { return term.Restore(fd, s) }
func (realTerm) GetSize(fd int) (w, h int, err error) { return term.GetSize(fd) }

// shell opens an interactive PTY-backed shell wired to the local terminal.
func shell(c *sshx.Client) error {
	return shellWith(c, realTerm{}, os.Stdin, os.Stdout, os.Stderr)
}

// shellWith is shell with the terminal and streams injected.
func shellWith(c *sshx.Client, tio termIO, stdin *os.File, stdout, stderr io.Writer) error {
	cfg := sshx.SessionConfig{Stdin: stdin, Stdout: stdout, Stderr: stderr}
	fd := int(stdin.Fd())
	if tio.IsTerminal(fd) {
		state, err := tio.MakeRaw(fd)
		if err != nil {
			return err
		}
		defer func() { _ = tio.Restore(fd, state) }()
		w, h, err := tio.GetSize(fd)
		if err != nil {
			w, h = 80, 24
		}
		cfg.TTY = &sshx.TTYConfig{Term: termType(), Cols: w, Rows: h}
	}
	sess, err := c.Shell(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	if cfg.TTY != nil {
		stop := make(chan struct{})
		defer close(stop)
		watchResize(func() {
			if w, h, gerr := tio.GetSize(fd); gerr == nil {
				_ = sess.Resize(w, h)
			}
		}, stop)
	}
	return sess.Wait()
}

// termType picks the terminal type to request remotely, defaulting sensibly.
func termType() string {
	if t := os.Getenv("TERM"); t != "" {
		return t
	}
	return "xterm-256color"
}
