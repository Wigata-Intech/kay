// White-box: confirmHost, dialHint, and termType are unexported glue whose
// prompt/refuse/hint paths must be covered without a real terminal or server.
package main

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/keys"

	sshx "github.com/Wigata-Intech/w-tools/x/sshx"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

var (
	errUnknownKey    = errors.New("unknown public key")
	errWrongPassword = errors.New("wrong password")
)

// newKeyedStore builds a real config store under a temp KAY_HOME holding one
// generated key named "id", returning the store and the key's public half.
func newKeyedStore(t *testing.T) (*config.Store, ssh.PublicKey) {
	t.Helper()
	t.Setenv("KAY_HOME", t.TempDir())
	st, err := config.Load()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	pair, err := keys.Generate(config.KeyEd25519, 0, "test")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	privPath, pubPath, err := pair.Write(st.KeysDir(), "id")
	if err != nil {
		t.Fatalf("write pair: %v", err)
	}
	if err := st.AddKey(config.Key{Name: "id", Type: config.KeyEd25519, PrivatePath: privPath, PublicPath: pubPath, Fingerprint: pair.Fingerprint}); err != nil {
		t.Fatalf("add key: %v", err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(pair.PublicAuth)
	if err != nil {
		t.Fatalf("parse pub: %v", err)
	}
	return st, pub
}

// addServer registers addr in the store under alias using key "id".
func addServer(t *testing.T, st *config.Store, alias, addr string) *config.Server {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	srv := config.Server{Alias: alias, Host: host, Port: port, User: "tester", KeyName: "id"}
	if err := st.AddServer(srv); err != nil {
		t.Fatalf("add server: %v", err)
	}
	return &srv
}

// devNull opens a writable /dev/null file for shell streams.
func devNull(t *testing.T) *os.File {
	t.Helper()
	f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// waitFor polls cond until it holds, failing the test at timeout.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

var errSizeFailed = errors.New("size failed")

// fakeTerm scripts termIO for shellWith tests and records the calls.
type fakeTerm struct {
	mu        sync.Mutex
	isTTY     bool
	rawErr    error
	sizeErr   error
	w, h      int
	madeRaw   bool
	restored  bool
	sizeCalls int
}

func (f *fakeTerm) IsTerminal(int) bool { return f.isTTY }
func (f *fakeTerm) MakeRaw(int) (*term.State, error) {
	if f.rawErr != nil {
		return nil, f.rawErr
	}
	f.madeRaw = true
	return &term.State{}, nil
}
func (f *fakeTerm) Restore(int, *term.State) error { f.restored = true; return nil }
func (f *fakeTerm) GetSize(int) (int, int, error) {
	f.mu.Lock()
	f.sizeCalls++
	f.mu.Unlock()
	if f.sizeErr != nil {
		return 0, 0, f.sizeErr
	}
	return f.w, f.h, nil
}

func (f *fakeTerm) getSizeCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sizeCalls
}

func testHostInfo() sshx.HostInfo {
	return sshx.HostInfo{
		Host:        "example.com:22",
		KeyType:     "ssh-ed25519",
		Fingerprint: "SHA256:testfingerprintvalue",
	}
}

func TestConfirmHost(t *testing.T) {
	h := testHostInfo()
	tests := []struct {
		name  string
		isTTY bool
		input string
		want  bool
	}{
		{name: "yes trusts", isTTY: true, input: "yes\n", want: true},
		{name: "y trusts", isTTY: true, input: "y\n", want: true},
		{name: "mixed case trusts", isTTY: true, input: "YES\n", want: true},
		{name: "no refuses", isTTY: true, input: "no\n", want: false},
		{name: "empty refuses", isTTY: true, input: "\n", want: false},
		{name: "no terminal refuses", isTTY: false, input: "yes\n", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			got, err := confirmHost(strings.NewReader(tt.input), &out, tt.isTTY, h)
			if err != nil {
				t.Fatalf("confirmHost error = %v", err)
			}
			if got != tt.want {
				t.Errorf("confirmHost = %v, want %v", got, tt.want)
			}
			if !tt.isTTY && !strings.Contains(out.String(), "no terminal to confirm") {
				t.Errorf("no-TTY message missing: %q", out.String())
			}
			if tt.isTTY && !strings.Contains(out.String(), h.Fingerprint) {
				t.Errorf("prompt should show the fingerprint: %q", out.String())
			}
		})
	}
}

func TestDialHint(t *testing.T) {
	srv := &config.Server{Alias: "web", Host: "host", Port: 22, User: "bob"}
	errPlain := errors.New("connection refused")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "hostkey stage",
			err:  &sshx.DialError{Stage: sshx.StageHostKey, Addr: "host:22", Err: &sshx.UnknownHostKeyError{Host: "host:22", KeyType: "ssh-ed25519", Fingerprint: "SHA256:x"}},
			want: "host key check failed",
		},
		{
			name: "handshake auth failure carries the hint",
			err:  &sshx.DialError{Stage: sshx.StageHandshake, Addr: "host:22", Err: errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [publickey]")},
			want: "is the public key in the server's authorized_keys?",
		},
		{
			name: "handshake non-auth failure gets no hint",
			err:  &sshx.DialError{Stage: sshx.StageHandshake, Addr: "host:22", Err: errors.New("ssh: handshake failed: EOF")},
			want: "cannot connect",
		},
		{
			name: "network stage",
			err:  &sshx.DialError{Stage: sshx.StageNetwork, Addr: "host:22", Err: errPlain},
			want: "cannot connect",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dialHint(tt.err, srv, "is the public key in the server's authorized_keys?")
			if !strings.Contains(got.Error(), tt.want) {
				t.Errorf("dialHint = %q, want substring %q", got, tt.want)
			}
		})
	}

	t.Run("non-DialError passes through unchanged", func(t *testing.T) {
		if got := dialHint(errPlain, srv, "hint"); got != errPlain { //nolint:errorlint // pass-through contract is identity
			t.Errorf("dialHint = %v, want the original error", got)
		}
	})
}

func TestTermType(t *testing.T) {
	tests := []struct {
		name string
		term string
		want string
	}{
		{name: "uses TERM when set", term: "screen-256color", want: "screen-256color"},
		{name: "falls back when empty", term: "", want: "xterm-256color"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM", tt.term)
			if got := termType(); got != tt.want {
				t.Errorf("termType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDialWith(t *testing.T) {
	t.Run("connects with the stored key and runs commands", func(t *testing.T) {
		st, pub := newKeyedStore(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
		srv := addServer(t, st, "test", server.addr())

		c, err := dialWith(context.Background(), st, srv, server.hostKey())
		if err != nil {
			t.Fatalf("dialWith() error = %v", err)
		}
		defer func() { _ = c.Close() }()
		out, err := runner{c}.Run("hello")
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if out != "ok:hello" {
			t.Errorf("Run() = %q, want %q", out, "ok:hello")
		}
	})

	t.Run("auth rejection carries the authorized_keys hint", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: newTestSigner(t).PublicKey()})
		srv := addServer(t, st, "test", server.addr())

		_, err := dialWith(context.Background(), st, srv, server.hostKey())
		if err == nil || !strings.Contains(err.Error(), "authorized_keys") {
			t.Errorf("dialWith() error = %v, want the authorized_keys hint", err)
		}
	})

	t.Run("unknown key name fails before dialing", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		srv := addServer(t, st, "test", "127.0.0.1:1")
		srv.KeyName = "ghost"
		if _, err := dialWith(context.Background(), st, srv, sshx.InsecureAcceptAny()); err == nil {
			t.Error("dialWith() error = nil, want unknown-key failure")
		}
	})

	t.Run("unreadable key file names the path", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		if err := st.AddKey(config.Key{Name: "bad", Type: config.KeyEd25519, PrivatePath: "/nonexistent/key", PublicPath: "/nonexistent/key.pub"}); err != nil {
			t.Fatalf("add key: %v", err)
		}
		srv := addServer(t, st, "test", "127.0.0.1:1")
		srv.KeyName = "bad"
		_, err := dialWith(context.Background(), st, srv, sshx.InsecureAcceptAny())
		if err == nil || !strings.Contains(err.Error(), "load key") {
			t.Errorf("dialWith() error = %v, want load-key context", err)
		}
	})
}

func TestDial(t *testing.T) {
	t.Run("insecure connects without pinning", func(t *testing.T) {
		st, pub := newKeyedStore(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
		srv := addServer(t, st, "test", server.addr())

		c, err := dial(st, srv, true)
		if err != nil {
			t.Fatalf("dial(insecure) error = %v", err)
		}
		_ = c.Close()
	})

	t.Run("unwritable known_hosts fails the policy", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		srv := addServer(t, st, "test", "127.0.0.1:1")
		if os.Geteuid() == 0 {
			t.Skip("chmod cannot block writes for root")
		}
		home := os.Getenv("KAY_HOME")
		if err := os.Chmod(home, 0o500); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
		if _, err := dial(st, srv, false); err == nil {
			t.Error("dial() error = nil, want host-key policy failure")
		}
	})

	t.Run("unknown host refused without a terminal", func(t *testing.T) {
		st, pub := newKeyedStore(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
		srv := addServer(t, st, "test", server.addr())

		_, err := dial(st, srv, false)
		if err == nil || !strings.Contains(err.Error(), "host key check failed") {
			t.Fatalf("dial() error = %v, want host key refusal", err)
		}
		if _, serr := os.Stat(st.KnownHostsPath()); serr != nil {
			t.Errorf("known_hosts not created: %v", serr)
		}
	})
}

func TestHostKeyPolicy(t *testing.T) {
	st, _ := newKeyedStore(t)
	tests := []struct {
		name     string
		insecure bool
	}{
		{name: "tofu by default", insecure: false},
		{name: "insecure on request", insecure: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, err := hostKeyPolicy(st, tt.insecure)
			if err != nil {
				t.Fatalf("hostKeyPolicy() error = %v", err)
			}
			if cb == nil {
				t.Error("hostKeyPolicy() = nil callback")
			}
		})
	}
}

func TestShellWith(t *testing.T) {
	newClient := func(t *testing.T) *sshx.Client {
		t.Helper()
		st, pub := newKeyedStore(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
		srv := addServer(t, st, "test", server.addr())
		c, err := dialWith(context.Background(), st, srv, server.hostKey())
		if err != nil {
			t.Fatalf("dialWith() error = %v", err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}

	t.Run("no terminal runs without a pty", func(t *testing.T) {
		st, pub := newKeyedStore(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
		srv := addServer(t, st, "test", server.addr())
		c, err := dialWith(context.Background(), st, srv, server.hostKey())
		if err != nil {
			t.Fatalf("dialWith() error = %v", err)
		}
		defer func() { _ = c.Close() }()

		f := &fakeTerm{isTTY: false}
		if err := shellWith(c, f, devNull(t), devNull(t), devNull(t)); err != nil {
			t.Fatalf("shellWith() error = %v", err)
		}
		if f.madeRaw {
			t.Error("raw mode entered without a terminal")
		}
		if got := server.ptyRecords(); len(got) != 0 {
			t.Errorf("pty requested without a terminal: %v", got)
		}
	})

	t.Run("terminal requests a pty at its size", func(t *testing.T) {
		st, pub := newKeyedStore(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
		srv := addServer(t, st, "test", server.addr())
		c, err := dialWith(context.Background(), st, srv, server.hostKey())
		if err != nil {
			t.Fatalf("dialWith() error = %v", err)
		}
		defer func() { _ = c.Close() }()

		t.Setenv("TERM", "vt220")
		f := &fakeTerm{isTTY: true, w: 120, h: 40}
		if err := shellWith(c, f, devNull(t), devNull(t), devNull(t)); err != nil {
			t.Fatalf("shellWith() error = %v", err)
		}
		if !f.madeRaw || !f.restored {
			t.Errorf("raw/restore = %v/%v, want true/true", f.madeRaw, f.restored)
		}
		waitFor(t, 2*time.Second, func() bool {
			for _, p := range server.ptyRecords() {
				if p.term == "vt220" && p.cols == 120 && p.rows == 40 {
					return true
				}
			}
			return false
		})
	})

	t.Run("size failure falls back to 80x24", func(t *testing.T) {
		st, pub := newKeyedStore(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
		srv := addServer(t, st, "test", server.addr())
		c, err := dialWith(context.Background(), st, srv, server.hostKey())
		if err != nil {
			t.Fatalf("dialWith() error = %v", err)
		}
		defer func() { _ = c.Close() }()

		f := &fakeTerm{isTTY: true, sizeErr: errSizeFailed}
		if err := shellWith(c, f, devNull(t), devNull(t), devNull(t)); err != nil {
			t.Fatalf("shellWith() error = %v", err)
		}
		waitFor(t, 2*time.Second, func() bool {
			for _, p := range server.ptyRecords() {
				if p.cols == 80 && p.rows == 24 {
					return true
				}
			}
			return false
		})
	})

	t.Run("closed client fails to open a session", func(t *testing.T) {
		c := newClient(t)
		_ = c.Close()
		f := &fakeTerm{isTTY: false}
		if err := shellWith(c, f, devNull(t), devNull(t), devNull(t)); !errors.Is(err, sshx.ErrClosed) {
			t.Errorf("shellWith() error = %v, want ErrClosed", err)
		}
	})

	t.Run("raw mode failure aborts before connecting", func(t *testing.T) {
		c := newClient(t)
		rawErr := errors.New("raw failed")
		f := &fakeTerm{isTTY: true, rawErr: rawErr}
		if err := shellWith(c, f, devNull(t), devNull(t), devNull(t)); !errors.Is(err, rawErr) {
			t.Errorf("shellWith() error = %v, want the raw-mode failure", err)
		}
		if f.restored {
			t.Error("restore called after raw-mode failure")
		}
	})
}

func TestRealTerm(t *testing.T) {
	fd := int(devNull(t).Fd())
	rt := realTerm{}
	if rt.IsTerminal(fd) {
		t.Error("IsTerminal(devnull) = true, want false")
	}
	if _, err := rt.MakeRaw(fd); err == nil {
		t.Error("MakeRaw(devnull) error = nil, want failure")
	}
	if err := rt.Restore(fd, &term.State{}); err == nil {
		t.Error("Restore(devnull) error = nil, want failure")
	}
	if _, _, err := rt.GetSize(fd); err == nil {
		t.Error("GetSize(devnull) error = nil, want failure")
	}
}

func TestShell(t *testing.T) {
	st, pub := newKeyedStore(t)
	server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
	srv := addServer(t, st, "test", server.addr())
	c, err := dialWith(context.Background(), st, srv, server.hostKey())
	if err != nil {
		t.Fatalf("dialWith() error = %v", err)
	}
	defer func() { _ = c.Close() }()
	// The test process has no TTY, so this exercises the plain-stream path
	// end to end through the real terminal implementation.
	if err := shell(c); err != nil {
		t.Errorf("shell() error = %v", err)
	}
}
