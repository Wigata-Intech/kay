package sshx_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/sshx"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// canned is the fixed output the test server writes for every exec request.
const canned = "hello from test server\n"

// testServer is an in-process SSH server backed by a loopback listener. It
// accepts a single client public key and answers exec requests with canned
// output. Everything runs over 127.0.0.1 so the tests touch no real network.
type testServer struct {
	addr       string     // host:port the server is listening on
	hostSigner ssh.Signer // host key, so tests can pin it in known_hosts
}

// newTestServer generates fresh host and client ed25519 keys, starts the
// server on a random loopback port, and registers cleanup that closes the
// listener and waits for accept goroutines to drain (so -race stays clean).
func newTestServer(t *testing.T) (*testServer, ssh.Signer) {
	t.Helper()

	hostSigner := newSigner(t)
	clientSigner := newSigner(t)

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			// Faithful to production: only the registered key is accepted.
			if string(key.Marshal()) == string(clientSigner.PublicKey().Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("unknown public key")
		},
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// Track accepted conns so cleanup can close them from the server side. The
	// tests deliberately do not call Client.Close (which races with the
	// keepalive goroutine in production), so the server must tear the
	// connection down itself to let serveConn return and the WaitGroup drain.
	var mu sync.Mutex
	var conns []net.Conn

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				serveConn(conn, cfg)
			}()
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})

	return &testServer{
		addr:       ln.Addr().String(),
		hostSigner: hostSigner,
	}, clientSigner
}

// serveConn completes the SSH handshake and services session channels until the
// connection goes away. exec requests get the canned output and exit-status 0.
func serveConn(conn net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return // handshake/auth failure; nothing more to do
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)

	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only session channels")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			return
		}
		go handleSession(ch, chReqs)
	}
}

// handleSession answers exec by writing canned output and closing with a clean
// exit status. pty-req/shell are acknowledged so an interactive path wouldn't
// hang, but the tests here exercise only exec via Run.
func handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "exec":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			_, _ = ch.Write([]byte(canned))
			// exit-status payload is a 4-byte big-endian status code (0).
			_, _ = ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
			_ = ch.Close()
			return
		case "pty-req", "shell":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer
}

// TestDialRun covers the happy path: an Insecure dial followed by Run, which
// returns the server's canned output, then a clean Close.
func TestDialRun(t *testing.T) {
	srv, clientSigner := newTestServer(t)

	client, err := sshx.Dial(sshx.DialOptions{
		Addr:     srv.addr,
		User:     "tester",
		Signer:   clientSigner,
		Insecure: true,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	out, err := client.Run("echo ignored")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != canned {
		t.Errorf("Run output = %q, want %q", out, canned)
	}
}

// TestClose verifies Close stops the keepalive goroutine without racing it, and
// is idempotent (safe to call more than once).
func TestClose(t *testing.T) {
	srv, clientSigner := newTestServer(t)

	client, err := sshx.Dial(sshx.DialOptions{
		Addr: srv.addr, User: "tester", Signer: clientSigner, Insecure: true, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Second Close must not panic; an error from the already-closed underlying
	// connection is acceptable.
	_ = client.Close()
	// Commands after Close should fail rather than hang.
	if _, err := client.Run("echo x"); err == nil {
		t.Error("Run after Close should error")
	}
}

// TestDialKnownHostsPinned covers the non-insecure verification path with a
// pre-populated known_hosts. TOFU auto-accept isn't exercised because
// confirmNewHost refuses without an interactive terminal (as under `go test`),
// so the host key is pinned up front to reach the success branch.
func TestDialKnownHostsPinned(t *testing.T) {
	srv, clientSigner := newTestServer(t)

	khPath := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(srv.addr)}, srv.hostSigner.PublicKey())
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("seed known_hosts: %v", err)
	}

	client, err := sshx.Dial(sshx.DialOptions{
		Addr:           srv.addr,
		User:           "tester",
		Signer:         clientSigner,
		KnownHostsPath: khPath,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial with pinned host key: %v", err)
	}
	// Close is omitted for the same reason as TestDialRun (Close vs keepalive
	// data race in production).

	out, err := client.Run("uname -a")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != canned {
		t.Errorf("Run output = %q, want %q", out, canned)
	}
}

// TestDialErrors covers the failure paths that don't need an interactive
// terminal: a missing known_hosts path, an unauthorized key, and an
// unreachable address.
func TestDialErrors(t *testing.T) {
	srv, clientSigner := newTestServer(t)

	// A signer whose public key the server does not accept.
	badSigner := newSigner(t)

	// A closed loopback port yields an immediate connection-refused.
	closedLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closedAddr := closedLn.Addr().String()
	_ = closedLn.Close()

	tests := []struct {
		name string
		opts sshx.DialOptions
	}{
		{
			name: "known_hosts required without insecure",
			opts: sshx.DialOptions{Addr: srv.addr, User: "tester", Signer: clientSigner},
		},
		{
			name: "unauthorized key",
			opts: sshx.DialOptions{Addr: srv.addr, User: "tester", Signer: badSigner, Insecure: true, Timeout: 5 * time.Second},
		},
		{
			name: "no auth method",
			opts: sshx.DialOptions{Addr: srv.addr, User: "tester", Insecure: true, Timeout: 5 * time.Second},
		},
		{
			name: "connection refused",
			opts: sshx.DialOptions{Addr: closedAddr, User: "tester", Signer: clientSigner, Insecure: true, Timeout: 2 * time.Second},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := sshx.Dial(tt.opts)
			if err == nil {
				_ = client.Close()
				t.Fatalf("Dial(%s) = nil error, want error", tt.name)
			}
		})
	}
}
