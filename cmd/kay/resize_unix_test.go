//go:build unix

// White-box: SIGWINCH delivery can only be exercised where the signal exists.
package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestWatchResize(t *testing.T) {
	fired := make(chan struct{}, 1)
	stop := make(chan struct{})
	defer close(stop)
	watchResize(func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	}, stop)

	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatalf("send SIGWINCH: %v", err)
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("resize callback never fired after SIGWINCH")
	}
}

func TestShellWithResize(t *testing.T) {
	st, pub := newKeyedStore(t)
	server := newTestSSHServer(t, testServerOpts{authorizedKey: pub, shellLinger: true})
	srv := addServer(t, st, "test", server.addr())
	c, err := dialWith(context.Background(), st, srv, server.hostKey())
	if err != nil {
		t.Fatalf("dialWith() error = %v", err)
	}
	defer func() { _ = c.Close() }()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = stdinW.Close() }()
	defer func() { _ = stdinR.Close() }()

	f := &fakeTerm{isTTY: true, w: 150, h: 45}
	done := make(chan error, 1)
	go func() { done <- shellWith(c, f, stdinR, stdinR, devNull(t), devNull(t)) }()

	// The pty must exist before the resize means anything.
	waitFor(t, 2*time.Second, func() bool { return len(server.ptyRecords()) > 0 })
	// Resent inside the poll: a one-shot signal can race the handler's
	// registration and be dropped.
	waitFor(t, 2*time.Second, func() bool {
		_ = syscall.Kill(os.Getpid(), syscall.SIGWINCH)
		for _, w := range server.winchRecords() {
			if w.cols == 150 && w.rows == 45 {
				return true
			}
		}
		return false
	})

	// A byte on stdin lets the lingering remote shell exit cleanly.
	if _, err := stdinW.Write([]byte("\n")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shellWith() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shellWith did not return after shell exit")
	}
}

func TestShellWithResizeSizeFailure(t *testing.T) {
	st, pub := newKeyedStore(t)
	server := newTestSSHServer(t, testServerOpts{authorizedKey: pub, shellLinger: true})
	srv := addServer(t, st, "test", server.addr())
	c, err := dialWith(context.Background(), st, srv, server.hostKey())
	if err != nil {
		t.Fatalf("dialWith() error = %v", err)
	}
	defer func() { _ = c.Close() }()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = stdinW.Close() }()
	defer func() { _ = stdinR.Close() }()

	// GetSize always fails: the pty falls back to 80x24 and a resize signal
	// must be swallowed rather than sent with garbage.
	f := &fakeTerm{isTTY: true, sizeErr: errSizeFailed}
	done := make(chan error, 1)
	go func() { done <- shellWith(c, f, stdinR, stdinR, devNull(t), devNull(t)) }()

	waitFor(t, 2*time.Second, func() bool { return len(server.ptyRecords()) > 0 })
	before := f.getSizeCalls()
	// Positive half: prove the handler actually ran (a fresh GetSize call
	// after the signal), resending in the poll to survive a dropped one-shot.
	waitFor(t, 2*time.Second, func() bool {
		_ = syscall.Kill(os.Getpid(), syscall.SIGWINCH)
		return f.getSizeCalls() > before
	})
	// Absence half: the handler ran but its size read failed, so no
	// window-change may have been sent.
	if got := server.winchRecords(); len(got) != 0 {
		t.Errorf("window-change sent despite size failure: %v", got)
	}

	if _, err := stdinW.Write([]byte("\n")); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shellWith() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shellWith did not return")
	}
}
