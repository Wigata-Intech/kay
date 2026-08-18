// White-box (package main): these tests cover unexported CLI helpers plus the
// main dispatch itself, driven through the exit/stdin seams.
package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// exitPanic carries the code passed to the stubbed exit seam.
type exitPanic struct{ code int }

// runMain runs main() with os.Args set and exit stubbed to panic, reporting
// whether (and with what code) main tried to terminate the process.
func runMain(t *testing.T, args ...string) (code int, exited bool) {
	t.Helper()
	oldArgs := os.Args
	oldExit := exit
	t.Cleanup(func() { os.Args = oldArgs; exit = oldExit })
	os.Args = append([]string{"kay"}, args...)
	exit = func(c int) { panic(exitPanic{c}) }
	defer func() {
		switch r := recover().(type) {
		case nil:
		case exitPanic:
			code, exited = r.code, true
		default:
			panic(r)
		}
	}()
	main()
	return code, exited
}

func TestMainDispatch(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantExit bool
	}{
		{name: "version", args: []string{"version"}, wantCode: 0, wantExit: false},
		{name: "help", args: []string{"help"}, wantCode: 0, wantExit: false},
		{name: "subcommand help is swallowed", args: []string{"connect", "-h"}, wantCode: 0, wantExit: false},
		{name: "no args prints usage", args: nil, wantCode: 2, wantExit: true},
		{name: "unknown command", args: []string{"bogus"}, wantCode: 1, wantExit: true},
		{name: "handler error", args: []string{"key"}, wantCode: 1, wantExit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KAY_HOME", t.TempDir())
			setIsTerminal(t, false) // pin the no-TTY dispatch: bare `kay` on a TTY opens the console
			var code int
			var exited bool
			_ = captureStdout(t, func() { code, exited = runMain(t, tt.args...) })
			if code != tt.wantCode || exited != tt.wantExit {
				t.Errorf("main(%v) = code %d, exited %v; want code %d, exited %v",
					tt.args, code, exited, tt.wantCode, tt.wantExit)
			}
		})
	}
}

func TestInputRouter(t *testing.T) {
	newRouter := func(t *testing.T) (*inputRouter, *os.File) {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		prev := stdinFile
		stdinFile = r
		t.Cleanup(func() { stdinFile = prev; _ = w.Close(); _ = r.Close() })
		return startInputRouter(), w
	}

	t.Run("decodes UI events and quits on stdin EOF", func(t *testing.T) {
		router, w := newRouter(t)
		if _, err := w.Write([]byte("q")); err != nil {
			t.Fatalf("write: %v", err)
		}
		ev := <-router.events
		if ev.Rune != 'q' {
			t.Errorf("event = %+v, want the q rune", ev)
		}
		_ = w.Close() // EOF must deliver a final quit, like a reader error
		select {
		case ev := <-router.events:
			if ev.Type != tui.EventQuit {
				t.Errorf("event after EOF = %+v, want EventQuit", ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no quit event after stdin EOF")
		}
	})

	t.Run("divert with buffered input at stdin EOF drains cleanly", func(t *testing.T) {
		router, w := newRouter(t)
		if _, err := w.Write([]byte(strings.Repeat("a", 20))); err != nil {
			t.Fatalf("write: %v", err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && len(router.events) < cap(router.events) {
			time.Sleep(time.Millisecond)
		}
		if _, err := w.Write([]byte("b")); err != nil {
			t.Fatalf("write: %v", err)
		}
		for time.Now().Before(deadline) && len(router.chunks) == 0 {
			time.Sleep(time.Millisecond)
		}
		_ = w.Close()                     // chunks closes behind the buffered b
		time.Sleep(20 * time.Millisecond) // let the reader observe EOF and close
		_, pw := io.Pipe()
		done := make(chan struct{})
		go func() { router.divertTo(pw); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("divertTo hung on a closed chunk stream")
		}
	})

	t.Run("divert wins even when the UI is not draining", func(t *testing.T) {
		router, w := newRouter(t)
		// Overfill the event buffer so the route loop blocks on delivery
		// (confirmed by the buffer reaching capacity) before diverting.
		if _, err := w.Write([]byte(strings.Repeat("a", 20))); err != nil {
			t.Fatalf("write: %v", err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && len(router.events) < cap(router.events) {
			time.Sleep(time.Millisecond)
		}
		// A separate write lands as its own chunk behind the blocked route
		// loop — it must be dropped by the switch, not fed to the shell.
		if _, err := w.Write([]byte("b")); err != nil {
			t.Fatalf("write: %v", err)
		}
		for time.Now().Before(deadline) && len(router.chunks) == 0 {
			time.Sleep(time.Millisecond)
		}
		pr, pw := io.Pipe()
		done := make(chan struct{})
		go func() { router.divertTo(pw); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("divertTo deadlocked against a full event buffer")
		}
		// The switch drain may eat a write that races the handoff (that IS the
		// typed-ahead drop), so keep writing z until one flows through raw.
		stop := make(chan struct{})
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = w.Write([]byte("z"))
					time.Sleep(10 * time.Millisecond)
				}
			}
		}()
		// Typed-ahead is dropped with the switch: the first raw byte must be
		// z, never the pre-divert b.
		buf := make([]byte, 1)
		if _, err := pr.Read(buf); err != nil || buf[0] != 'z' {
			t.Fatalf("diverted read = %q, %v; want z", buf, err)
		}
		close(stop)
		_ = pw.Close() // prod ordering: unblock any raw write, then un-divert
		router.divertTo(nil)
		if _, err := w.Write([]byte("k")); err != nil {
			t.Fatalf("write: %v", err)
		}
		// The switch back drained the stale a events; stray z stragglers are
		// our own noise, but no a may precede the fresh keystroke.
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case ev := <-router.events:
				switch ev.Rune {
				case 'k':
					return
				case 'a':
					t.Fatal("stale pre-divert event replayed after un-divert")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("events never resumed after un-divert")
			}
		}
		t.Fatal("fresh keystroke never arrived")
	})
}

// setStdin scripts the interactive selection prompt.
func setStdin(t *testing.T, input string) {
	t.Helper()
	old := stdinReader
	t.Cleanup(func() { stdinReader = old })
	stdinReader = strings.NewReader(input)
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "abc", "'abc'"},
		{"with space", "a b", "'a b'"},
		{"single quote", "it's", `'it'\''s'`},
		{"empty", "", "''"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestAnonEnabled(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{"unset", "", false},
		{"set", "1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KAY_DEMO", tt.env)
			if got := anonEnabled(); got != tt.want {
				t.Errorf("anonEnabled() with KAY_DEMO=%q = %v, want %v", tt.env, got, tt.want)
			}
		})
	}
}

func TestPickServer(t *testing.T) {
	// Isolate the store under a temp KAY_HOME and seed one key + two servers.
	t.Setenv("KAY_HOME", t.TempDir())
	st, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := st.AddKey(config.Key{Name: "k"}); err != nil {
		t.Fatalf("add key: %v", err)
	}
	if err := st.AddServer(config.Server{Alias: "a", Host: "h1", User: "u", KeyName: "k"}); err != nil {
		t.Fatalf("add server a: %v", err)
	}

	t.Run("single server auto-selects", func(t *testing.T) {
		srv, err := pickServer(st, "")
		if err != nil || srv.Alias != "a" {
			t.Errorf("pickServer(\"\") = %+v, %v; want alias a", srv, err)
		}
	})

	if err := st.AddServer(config.Server{Alias: "b", Host: "h2", User: "u", KeyName: "k"}); err != nil {
		t.Fatalf("add server b: %v", err)
	}

	t.Run("explicit alias", func(t *testing.T) {
		srv, err := pickServer(st, "b")
		if err != nil || srv.Alias != "b" {
			t.Errorf("pickServer(\"b\") = %+v, %v; want alias b", srv, err)
		}
	})

	t.Run("unknown alias errors", func(t *testing.T) {
		if _, err := pickServer(st, "missing"); err == nil {
			t.Error("expected error for unknown alias")
		}
	})

	t.Run("prompt selects by number", func(t *testing.T) {
		setStdin(t, "2\n")
		srv, err := pickServer(st, "")
		if err != nil || srv.Alias != "b" {
			t.Errorf("pickServer(prompt 2) = %+v, %v; want alias b", srv, err)
		}
	})

	t.Run("prompt rejects out-of-range selection", func(t *testing.T) {
		setStdin(t, "9\n")
		if _, err := pickServer(st, ""); err == nil {
			t.Error("expected error for out-of-range selection")
		}
	})

	t.Run("prompt rejects non-numeric selection", func(t *testing.T) {
		setStdin(t, "two\n")
		if _, err := pickServer(st, ""); err == nil {
			t.Error("expected error for non-numeric selection")
		}
	})

	t.Run("no servers errors", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir()) // fresh, empty store
		empty, err := config.Load()
		if err != nil {
			t.Fatalf("load empty: %v", err)
		}
		if _, err := pickServer(empty, ""); err == nil {
			t.Error("expected error when no servers registered")
		}
	})
}
