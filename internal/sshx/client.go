// Package sshx is the single SSH connection path used by the rest of kay.
// It dials with public-key auth, verifies host keys with trust-on-first-use,
// and exposes one-shot command execution and an interactive shell.
package sshx

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/term"
)

// DialOptions configures a connection.
type DialOptions struct {
	Addr           string // host:port
	User           string
	Signer         ssh.Signer
	Password       string // optional; enables password auth (e.g. assisted install)
	KnownHostsPath string
	Insecure       bool // skip host key verification (dangerous)
	Timeout        time.Duration
}

// Client wraps an *ssh.Client with the helpers kay needs.
type Client struct {
	c    *ssh.Client
	done chan struct{}
}

// Dial establishes an authenticated connection.
func Dial(opts DialOptions) (*Client, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}
	cb, err := hostKeyCallback(opts)
	if err != nil {
		return nil, err
	}
	var auth []ssh.AuthMethod
	if opts.Signer != nil {
		auth = append(auth, ssh.PublicKeys(opts.Signer))
	}
	if opts.Password != "" {
		auth = append(auth, ssh.Password(opts.Password))
	}
	if len(auth) == 0 {
		return nil, errors.New("no authentication method (need a key or password)")
	}
	cfg := &ssh.ClientConfig{
		User:            opts.User,
		Auth:            auth,
		HostKeyCallback: cb,
		Timeout:         opts.Timeout,
	}
	client, err := ssh.Dial("tcp", opts.Addr, cfg)
	if err != nil {
		return nil, classifyDialError(err, opts)
	}
	c := &Client{c: client, done: make(chan struct{})}
	go c.keepalive(15 * time.Second)
	return c, nil
}

// keepalive periodically pings the server so idle connections aren't dropped by
// the server's ClientAliveInterval. Exits when the client is closed.
func (c *Client) keepalive(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			if _, _, err := c.c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				return
			}
		}
	}
}

// Run executes a single command and returns its combined stdout+stderr output.
// This is the only place commands are executed, so metrics and `exec` share it.
func (c *Client) Run(cmd string) (string, error) {
	sess, err := c.c.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	return string(out), err
}

// Shell opens an interactive PTY-backed shell wired to the local terminal.
func (c *Client) Shell() error {
	sess, err := c.c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	sess.Stdin = os.Stdin
	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		state, err := term.MakeRaw(fd)
		if err != nil {
			return err
		}
		defer term.Restore(fd, state)
		w, h, err := term.GetSize(fd)
		if err != nil {
			w, h = 80, 24
		}
		modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
		if err := sess.RequestPty(termType(), h, w, modes); err != nil {
			return err
		}
	}
	if err := sess.Shell(); err != nil {
		return err
	}
	return sess.Wait()
}

// Close terminates the connection and stops the keepalive goroutine.
func (c *Client) Close() error {
	if c.done != nil {
		close(c.done)
		c.done = nil
	}
	return c.c.Close()
}

func termType() string {
	if t := os.Getenv("TERM"); t != "" {
		return t
	}
	return "xterm-256color"
}

// hostKeyCallback returns a verifier implementing trust-on-first-use against
// the known_hosts file, or an insecure no-op when requested.
func hostKeyCallback(opts DialOptions) (ssh.HostKeyCallback, error) {
	if opts.Insecure {
		return ssh.InsecureIgnoreHostKey(), nil //#nosec G106 -- explicit opt-in via the insecure flag; TOFU known_hosts is the default
	}
	path := opts.KnownHostsPath
	if path == "" {
		return nil, errors.New("known_hosts path required unless --insecure")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Ensure the file exists so knownhosts.New can open it.
	if f, err := os.OpenFile(path, os.O_CREATE, 0o600); err == nil { //#nosec G304 -- known_hosts path is kay-controlled, not untrusted input
		_ = f.Close()
	}
	verify, err := knownhosts.New(path)
	if err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := verify(hostname, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		if errors.As(err, &ke) && len(ke.Want) == 0 {
			// Unknown host: ask for explicit consent before trusting (TOFU).
			if !confirmNewHost(hostname, key) {
				return fmt.Errorf("host key for %s was not trusted; connection aborted", hostname)
			}
			return appendKnownHost(path, hostname, remote, key)
		}
		// Non-empty Want means the stored key changed: a possible MITM.
		return fmt.Errorf("host key verification failed for %s: %w", hostname, err)
	}, nil
}

// confirmNewHost shows the fingerprint of a previously-unseen host and asks the
// user to approve it. Refuses automatically when there is no terminal to ask.
func confirmNewHost(hostname string, key ssh.PublicKey) bool {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fmt.Fprintf(os.Stderr, "kay: unknown host %s and no terminal to confirm — refusing.\n", hostname)
		return false
	}
	fmt.Fprintf(os.Stderr, "The authenticity of host %s can't be established.\n", hostname)
	fmt.Fprintf(os.Stderr, "%s key fingerprint is %s\n", key.Type(), ssh.FingerprintSHA256(key))
	fmt.Fprint(os.Stderr, "Trust this host and continue connecting? (yes/no): ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "yes" || line == "y"
}

func appendKnownHost(path, hostname string, remote net.Addr, key ssh.PublicKey) error {
	addrs := []string{knownhosts.Normalize(hostname)}
	if remote != nil {
		if ra := knownhosts.Normalize(remote.String()); ra != addrs[0] {
			addrs = append(addrs, ra)
		}
	}
	line := knownhosts.Line(addrs, key) + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600) //#nosec G304 -- known_hosts path is kay-controlled, not untrusted input
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(os.Stderr, "kay: added %s to known_hosts\n", hostname)
	_, err = io.WriteString(f, line)
	return err
}

// classifyDialError turns low-level dial errors into actionable messages.
func classifyDialError(err error, opts DialOptions) error {
	msg := err.Error()
	switch {
	case contains(msg, "unable to authenticate"), contains(msg, "no supported methods"):
		return fmt.Errorf("authentication failed for %s@%s: is the public key in the server's authorized_keys?",
			opts.User, opts.Addr)
	case contains(msg, "host key verification"), contains(msg, "knownhosts"):
		return fmt.Errorf("host key check failed for %s: %w (use --insecure to override)", opts.Addr, err)
	default:
		return fmt.Errorf("cannot connect to %s: %w", opts.Addr, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
