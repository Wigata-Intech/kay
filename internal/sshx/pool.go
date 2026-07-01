package sshx

import (
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// This file is the persistent-connection layer: instead of dialing a fresh
// connection for every command, a Managed keeps one connection alive per host
// and reuses it for many sessions (x/crypto/ssh multiplexes sessions over one
// transport, so a reused connection skips the KEX + auth handshake entirely).
//
// It depends only on the standard library and golang.org/x/crypto/ssh — like
// the rest of sshx it imports nothing app-specific, so this whole package can
// later be lifted into a shared module unchanged.

// ErrNotReady is returned by Managed.Run when the underlying connection is not
// currently established (it is connecting, backing off, or reconnecting).
var ErrNotReady = errors.New("ssh connection not ready")

// ErrPoolClosed is returned when an operation is attempted after Close.
var ErrPoolClosed = errors.New("ssh pool closed")

// ConnState is the lifecycle state of a Managed connection.
type ConnState int

const (
	// StateConnecting is the initial state before the first dial resolves.
	StateConnecting ConnState = iota
	// StateReady means a live connection is established and usable.
	StateReady
	// StateBroken means the last attempt failed; a reconnect is scheduled.
	StateBroken
)

// String renders the state for logs and status lines.
func (s ConnState) String() string {
	switch s {
	case StateReady:
		return "ready"
	case StateBroken:
		return "broken"
	default:
		return "connecting"
	}
}

// conn is the minimal capability a Managed needs from a connection. *Client
// satisfies it; tests inject a fake so the pool can be exercised without a real
// server.
type conn interface {
	Run(cmd string) (string, error)
	Close() error
}

// pinger is the optional health-probe capability. *Client implements it; a
// connection that doesn't is simply never actively probed (its health is still
// detected through Run failures).
type pinger interface {
	Ping() error
}

// Backoff tuning for reconnect attempts (exponential with full jitter).
const (
	backoffBase = 500 * time.Millisecond
	backoffCap  = 30 * time.Second
	probeEvery  = 15 * time.Second
)

// Pool owns a set of Managed connections and the dial concurrency limit shared
// across them. Capping concurrent dials keeps a cold start of many hosts from
// tripping a server's sshd MaxStartups throttle or exhausting local sockets.
type Pool struct {
	sem chan struct{} // dial-concurrency semaphore

	mu    sync.Mutex
	conns []*Managed
	dead  bool
}

// NewPool returns a pool that permits at most maxDials concurrent dials across
// all its connections. A non-positive maxDials defaults to 16.
func NewPool(maxDials int) *Pool {
	if maxDials <= 0 {
		maxDials = 16
	}
	return &Pool{sem: make(chan struct{}, maxDials)}
}

// Add registers a host and starts maintaining a connection to it in the
// background, returning immediately. dial is called whenever a (re)connection is
// needed. The returned Managed is usable at once (Run reports ErrNotReady until
// the first dial succeeds).
func (p *Pool) Add(dial func() (*Client, error)) *Managed {
	m := &Managed{
		pool:      p,
		dialFn:    func() (conn, error) { return adaptDial(dial) },
		reconnect: make(chan struct{}, 1),
		done:      make(chan struct{}),
		baseDelay: backoffBase,
		maxDelay:  backoffCap,
		probe:     probeEvery,
	}
	p.mu.Lock()
	closed := p.dead
	if !closed {
		p.conns = append(p.conns, m)
	}
	p.mu.Unlock()
	if closed {
		m.closeOnce.Do(func() { close(m.done) })
		return m
	}
	go m.manage()
	return m
}

// adaptDial runs the caller's *Client dial and returns it as a conn, preserving
// a nil interface (not a typed-nil) on error.
func adaptDial(dial func() (*Client, error)) (conn, error) {
	c, err := dial()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Close tears down every Managed connection. It is safe to call once; further
// Add calls return an already-closed Managed.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.dead {
		p.mu.Unlock()
		return
	}
	p.dead = true
	cs := p.conns
	p.conns = nil
	p.mu.Unlock()
	for _, m := range cs {
		m.Close()
	}
}

// Managed is a self-healing connection to one host: it keeps a single live
// connection, reconnecting with exponential backoff and jitter after a failure,
// and hands the live connection to callers. All command execution reuses the
// same transport, so only the first dial pays the handshake cost.
type Managed struct {
	pool   *Pool
	dialFn func() (conn, error)

	reconnect chan struct{} // buffered(1): request a redial
	done      chan struct{}
	closeOnce sync.Once

	// backoff/probe timings; defaulted from package constants in Add, overridable
	// in tests for fast, deterministic runs.
	baseDelay time.Duration
	maxDelay  time.Duration
	probe     time.Duration

	mu      sync.Mutex
	client  conn
	state   ConnState
	lastErr error
}

// State returns the current lifecycle state.
func (m *Managed) State() ConnState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Err returns the error from the last failed attempt, or nil.
func (m *Managed) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

// Client returns the live *Client for direct reuse (e.g. drilling into a host's
// dashboard without a second handshake), or nil when not currently ready.
func (m *Managed) Client() *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateReady {
		return nil
	}
	c, _ := m.client.(*Client)
	return c
}

// Run executes a command on the live connection, reusing the transport. It
// returns ErrNotReady immediately (without blocking) when no connection is
// currently established, so a caller polling many hosts never stalls on one
// that is down. A transport-level failure schedules a reconnect.
func (m *Managed) Run(cmd string) (string, error) {
	m.mu.Lock()
	c, ready := m.client, m.state == StateReady
	m.mu.Unlock()
	if !ready || c == nil {
		return "", ErrNotReady
	}
	out, err := c.Run(cmd)
	if fatalConnErr(err) {
		m.signalReconnect()
	}
	return out, err
}

// Close stops maintaining the connection and tears down the live transport.
func (m *Managed) Close() {
	m.closeOnce.Do(func() { close(m.done) })
}

// manage is the per-connection maintenance loop: dial (with backoff on failure),
// serve the live connection until it dies or a reconnect is requested, then
// repeat — until Close.
func (m *Managed) manage() {
	attempt := 0
	for {
		c, err := m.dial()
		if err != nil {
			if errors.Is(err, ErrPoolClosed) {
				return
			}
			attempt++
			m.set(StateBroken, nil, err)
			if !m.wait(m.backoff(attempt)) {
				return
			}
			continue
		}
		attempt = 0
		m.set(StateReady, c, nil)
		redial := m.serve(c)
		m.teardown(c)
		if !redial {
			return
		}
	}
}

// dial acquires a slot from the pool's dial semaphore (respecting Close) and
// runs the dial function.
func (m *Managed) dial() (conn, error) {
	select {
	case m.pool.sem <- struct{}{}:
	case <-m.done:
		return nil, ErrPoolClosed
	}
	defer func() { <-m.pool.sem }()
	return m.dialFn()
}

// serve blocks while c is the live connection, probing its health periodically.
// It returns true to request a redial (connection died or a reconnect was
// signalled) or false when the connection is being closed for good.
func (m *Managed) serve(c conn) bool {
	t := time.NewTicker(m.probe)
	defer t.Stop()
	p, canProbe := c.(pinger)
	for {
		select {
		case <-m.done:
			return false
		case <-m.reconnect:
			return true
		case <-t.C:
			if canProbe && p.Ping() != nil {
				m.set(StateBroken, nil, ErrNotReady)
				return true
			}
		}
	}
}

// teardown closes the live connection and clears it from the model.
func (m *Managed) teardown(c conn) {
	_ = c.Close()
	m.mu.Lock()
	if m.client == c {
		m.client = nil
	}
	m.mu.Unlock()
}

// signalReconnect marks the connection broken and requests a redial, unless one
// is already pending. Non-blocking.
func (m *Managed) signalReconnect() {
	m.mu.Lock()
	if m.state == StateReady {
		m.state = StateBroken
	}
	m.mu.Unlock()
	select {
	case m.reconnect <- struct{}{}:
	default:
	}
}

// set atomically updates the connection state, live client, and last error.
func (m *Managed) set(state ConnState, c conn, err error) {
	m.mu.Lock()
	m.state = state
	if c != nil || state == StateReady {
		m.client = c
	}
	m.lastErr = err
	m.mu.Unlock()
}

// wait sleeps for d, returning false if Close happened first.
func (m *Managed) wait(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-m.done:
		return false
	case <-t.C:
		return true
	}
}

// backoff returns an exponential backoff with full jitter, capped, so that many
// hosts recovering from a shared network blip don't reconnect in lockstep.
func (m *Managed) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 6) // base << 6 already dwarfs any sane cap
	d := m.baseDelay << shift
	if d > m.maxDelay || d <= 0 {
		d = m.maxDelay
	}
	//#nosec G404 -- jitter for reconnect spacing, not a security decision
	return time.Duration(rand.Int64N(int64(d))) + 1
}

// fatalConnErr reports whether err indicates the transport is gone (as opposed
// to the remote command merely exiting non-zero, which leaves the connection
// perfectly usable).
func fatalConnErr(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *ssh.ExitError
	var missingErr *ssh.ExitMissingError
	if errors.As(err, &exitErr) || errors.As(err, &missingErr) {
		return false
	}
	return true
}
