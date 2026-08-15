// White-box: an in-process SSH server so the glue in ssh.go is exercised
// against a real peer — handshakes, auth, exec, PTY and window-change on the
// wire — without a network or fixtures.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

type ptyGeom struct {
	term       string
	cols, rows uint32
}

type testServerOpts struct {
	authorizedKey ssh.PublicKey // nil: no public-key auth offered
	password      string        // non-empty: accept password auth with this value
	shellLinger   bool          // shell waits for one stdin byte before exiting
	execExit      int           // exit status returned for exec requests
}

type testSSHServer struct {
	t          *testing.T
	ln         net.Listener
	hostSigner ssh.Signer
	opts       testServerOpts

	mu     sync.Mutex
	ptys   []ptyGeom
	winchs []ptyGeom
	execs  []string
	closed bool

	wg sync.WaitGroup
}

func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer
}

func newTestSSHServer(t *testing.T, opts testServerOpts) *testSSHServer {
	t.Helper()
	s := &testSSHServer{t: t, hostSigner: newTestSigner(t), opts: opts}

	cfg := &ssh.ServerConfig{}
	if opts.authorizedKey == nil && opts.password == "" {
		cfg.NoClientAuth = true
	}
	if opts.authorizedKey != nil {
		want := string(ssh.MarshalAuthorizedKey(opts.authorizedKey))
		cfg.PublicKeyCallback = func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(ssh.MarshalAuthorizedKey(key)) == want {
				return nil, nil //nolint:nilnil // (nil, nil) is ssh.ServerConfig's documented success return
			}
			return nil, errUnknownKey
		}
	}
	if opts.password != "" {
		cfg.PasswordCallback = func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == opts.password {
				return nil, nil //nolint:nilnil // (nil, nil) is ssh.ServerConfig's documented success return
			}
			return nil, errWrongPassword
		}
	}
	cfg.AddHostKey(s.hostSigner)

	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.ln = ln
	s.wg.Add(1)
	go s.accept(cfg)
	t.Cleanup(s.close)
	return s
}

func (s *testSSHServer) addr() string { return s.ln.Addr().String() }

func (s *testSSHServer) hostKey() ssh.HostKeyCallback {
	return ssh.FixedHostKey(s.hostSigner.PublicKey())
}

func (s *testSSHServer) ptyRecords() []ptyGeom {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ptyGeom(nil), s.ptys...)
}

func (s *testSSHServer) winchRecords() []ptyGeom {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ptyGeom(nil), s.winchs...)
}

func (s *testSSHServer) execRecords() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.execs...)
}

func (s *testSSHServer) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.ln.Close()
	s.wg.Wait()
}

func (s *testSSHServer) accept(cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go s.handle(conn, cfg)
	}
}

func (s *testSSHServer) handle(conn net.Conn, cfg *ssh.ServerConfig) {
	defer s.wg.Done()
	sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer func() { _ = sconn.Close() }()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, chReqs, aerr := newCh.Accept()
		if aerr != nil {
			continue
		}
		s.wg.Add(1)
		go s.session(ch, chReqs)
	}
}

func (s *testSSHServer) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer s.wg.Done()
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		switch req.Type {
		case "exec":
			var p struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &p)
			s.mu.Lock()
			s.execs = append(s.execs, p.Command)
			s.mu.Unlock()
			_ = req.Reply(true, nil)
			_, _ = ch.Write([]byte("ok:" + p.Command))
			s.exit(ch, s.opts.execExit)
			return
		case "shell":
			_ = req.Reply(true, nil)
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				if s.opts.shellLinger {
					buf := make([]byte, 1)
					_, _ = ch.Read(buf)
				}
				s.exit(ch, 0)
				_ = ch.Close()
			}()
		case "pty-req":
			var p struct {
				Term         string
				Cols, Rows   uint32
				WPx, HPx     uint32
				EncodedModes string
			}
			if err := ssh.Unmarshal(req.Payload, &p); err == nil {
				s.mu.Lock()
				s.ptys = append(s.ptys, ptyGeom{term: p.Term, cols: p.Cols, rows: p.Rows})
				s.mu.Unlock()
			}
			_ = req.Reply(true, nil)
		case "window-change":
			var p struct {
				Cols, Rows uint32
				WPx, HPx   uint32
			}
			if err := ssh.Unmarshal(req.Payload, &p); err == nil {
				s.mu.Lock()
				s.winchs = append(s.winchs, ptyGeom{cols: p.Cols, rows: p.Rows})
				s.mu.Unlock()
			}
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

func (s *testSSHServer) exit(ch ssh.Channel, code int) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)})) //#nosec G115 -- test exit codes are tiny non-negatives
}
