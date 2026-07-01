// Command/package kaybench is a SEPARATE module (its own go.mod) so the rival
// SSH-pool libraries it imports never touch kay's dependency set. It benchmarks
// connection-reuse strategies against a REAL sshd, so every benchmark here is
// skipped unless the target is configured via environment:
//
//	BENCH_SSH_ADDR   host:port           (required, e.g. 127.0.0.1:22)
//	BENCH_SSH_USER   login user          (required)
//	BENCH_SSH_KEY    path to private key (required)
//
// Run:
//
//	BENCH_SSH_ADDR=127.0.0.1:22 BENCH_SSH_USER=$USER BENCH_SSH_KEY=~/.ssh/id_ed25519 \
//	  go test -bench=. -benchmem -run='^$' ./...
//
// Strategies compared (all run the same trivial remote command, `true`, to
// isolate transport/session overhead from the command itself):
//
//   - Kay-style reuse   one *ssh.Client, a fresh Session per op (mirrors
//     internal/sshx.Client.Run, which internal/ visibility
//     forbids importing from this module)
//   - desops/sshpool    per-host pool, ExecCombinedOutputString per op
//   - jolestar pool      generic object pool holding *ssh.Client; Borrow → Session → Return
//   - dial-per-op        a full dial+handshake every op — what kay's fleet did
//     before 0.2.0; the reference the reuse strategies must beat
package kaybench

import (
	"context"
	"os"
	"testing"
	"time"

	sshpool "github.com/desops/sshpool"
	pool "github.com/jolestar/go-commons-pool/v2"
	"golang.org/x/crypto/ssh"
)

const benchCmd = "true"

// target holds the resolved SSH target, or skips the benchmark if unconfigured.
type target struct {
	addr, user string
	cfg        *ssh.ClientConfig
}

func mustTarget(tb testing.TB) target {
	tb.Helper()
	addr := os.Getenv("BENCH_SSH_ADDR")
	user := os.Getenv("BENCH_SSH_USER")
	keyPath := os.Getenv("BENCH_SSH_KEY")
	if addr == "" || user == "" || keyPath == "" {
		tb.Skip("set BENCH_SSH_ADDR, BENCH_SSH_USER, BENCH_SSH_KEY to run against a real sshd")
	}
	pem, err := os.ReadFile(keyPath) //nolint:gosec // bench harness reads a key path the operator supplies
	if err != nil {
		tb.Fatalf("read key: %v", err)
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		tb.Fatalf("parse key: %v", err)
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // bench harness against a known local host
		Timeout:         10 * time.Second,
	}
	return target{addr: addr, user: user, cfg: cfg}
}

func (t target) dial(tb testing.TB) *ssh.Client {
	tb.Helper()
	c, err := ssh.Dial("tcp", t.addr, t.cfg)
	if err != nil {
		tb.Fatalf("dial: %v", err)
	}
	return c
}

// runSession opens a session on an existing client and runs the trivial command.
func runSession(tb testing.TB, c *ssh.Client) {
	tb.Helper()
	sess, err := c.NewSession()
	if err != nil {
		tb.Fatalf("new session: %v", err)
	}
	defer func() { _ = sess.Close() }()
	if _, err := sess.CombinedOutput(benchCmd); err != nil {
		tb.Fatalf("run: %v", err)
	}
}

// BenchmarkColdDialPerOp measures a full dial + handshake + one session every op.
// This is the cost the reuse strategies exist to avoid; it is the same handshake
// for every library, so one benchmark quantifies it.
func BenchmarkColdDialPerOp(b *testing.B) {
	t := mustTarget(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c := t.dial(b)
		runSession(b, c)
		_ = c.Close()
	}
}

// BenchmarkWarmKayReuse measures a session open on a single reused client — the
// kay strategy.
func BenchmarkWarmKayReuse(b *testing.B) {
	t := mustTarget(b)
	c := t.dial(b)
	defer func() { _ = c.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runSession(b, c)
	}
}

// BenchmarkReconnect measures the full recovery cost after a connection drops:
// close the client, redial (full handshake), and run one session. This is the
// one-off price a self-healing pool pays per reconnect — strategy-independent, so
// one number covers them all.
func BenchmarkReconnect(b *testing.B) {
	t := mustTarget(b)
	c := t.dial(b)
	defer func() { _ = c.Close() }()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = c.Close()
		c = t.dial(b)
		runSession(b, c)
	}
}

// BenchmarkWarmDesops measures the same op through desops/sshpool.
func BenchmarkWarmDesops(b *testing.B) {
	t := mustTarget(b)
	p := sshpool.New(t.cfg, &sshpool.PoolConfig{
		MaxConnections:    4,
		MaxSessions:       10,
		SessionCloseDelay: time.Millisecond, // avoid the default multi-ms serialization stall
	})
	defer p.Close()
	// Prime the pool so the first op isn't a cold dial.
	if _, err := p.ExecCombinedOutputString(t.addr, benchCmd); err != nil {
		b.Fatalf("prime: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := p.ExecCombinedOutputString(t.addr, benchCmd); err != nil {
			b.Fatalf("exec: %v", err)
		}
	}
}

// BenchmarkWarmJolestar measures the op through a generic go-commons-pool holding
// *ssh.Client: borrow a client, open a session, return it.
func BenchmarkWarmJolestar(b *testing.B) {
	t := mustTarget(b)
	ctx := context.Background()
	factory := pool.NewPooledObjectFactorySimple(func(context.Context) (any, error) {
		return t.dial(b), nil
	})
	p := pool.NewObjectPoolWithDefaultConfig(ctx, factory)
	defer p.Close(ctx)

	obj, err := p.BorrowObject(ctx) // prime one client
	if err != nil {
		b.Fatalf("borrow: %v", err)
	}
	_ = p.ReturnObject(ctx, obj)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		o, err := p.BorrowObject(ctx)
		if err != nil {
			b.Fatalf("borrow: %v", err)
		}
		runSession(b, o.(*ssh.Client))
		if err := p.ReturnObject(ctx, o); err != nil {
			b.Fatalf("return: %v", err)
		}
	}
}
