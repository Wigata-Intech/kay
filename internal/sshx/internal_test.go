// White-box: these cover unexported, non-interactive helpers (termType,
// contains, classifyDialError, hostKeyCallback, appendKnownHost) that the
// black-box client_test.go cannot reach through the exported API.
package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testPub(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return sshPub
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

func TestContains(t *testing.T) {
	tests := []struct {
		name string
		s    string
		sub  string
		want bool
	}{
		{name: "substring present", s: "unable to authenticate", sub: "authenticate", want: true},
		{name: "prefix", s: "knownhosts error", sub: "knownhosts", want: true},
		{name: "empty needle", s: "anything", sub: "", want: true},
		{name: "absent", s: "connection refused", sub: "timeout", want: false},
		{name: "needle longer than haystack", s: "hi", sub: "hello", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contains(tt.s, tt.sub); got != tt.want {
				t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.sub, got, tt.want)
			}
		})
	}
}

func TestClassifyDialError(t *testing.T) {
	opts := DialOptions{User: "bob", Addr: "host:22"}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "auth failure", err: errors.New("ssh: unable to authenticate"), want: "authentication failed"},
		{name: "no methods", err: errors.New("ssh: no supported methods remain"), want: "authentication failed"},
		{name: "host key mismatch", err: errors.New("host key verification failed"), want: "host key check failed"},
		{name: "knownhosts", err: errors.New("knownhosts: key mismatch"), want: "host key check failed"},
		{name: "generic falls through", err: errors.New("connection refused"), want: "cannot connect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyDialError(tt.err, opts).Error()
			if !strings.Contains(got, tt.want) {
				t.Errorf("classifyDialError = %q, want substring %q", got, tt.want)
			}
		})
	}
}

func TestHostKeyCallbackErrors(t *testing.T) {
	t.Run("insecure returns a callback", func(t *testing.T) {
		cb, err := hostKeyCallback(DialOptions{Insecure: true})
		if err != nil || cb == nil {
			t.Fatalf("insecure: cb=%v err=%v, want non-nil cb and nil err", cb, err)
		}
		if err := cb("anyhost:22", nil, testPub(t)); err != nil {
			t.Errorf("insecure callback should accept any key, got %v", err)
		}
	})
	t.Run("missing known_hosts path errors", func(t *testing.T) {
		_, err := hostKeyCallback(DialOptions{})
		if err == nil || !strings.Contains(err.Error(), "known_hosts path required") {
			t.Errorf("err = %v, want known_hosts path required", err)
		}
	})
}

func TestHostKeyCallbackPinning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	host := "example.com:22"
	remote := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 22}
	pinned := testPub(t)

	// Record the host, then a fresh callback must accept the same key and reject
	// a different one (a changed key looks like a possible MITM).
	if err := appendKnownHost(path, host, remote, pinned); err != nil {
		t.Fatalf("appendKnownHost: %v", err)
	}
	cb, err := hostKeyCallback(DialOptions{KnownHostsPath: path})
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if err := cb(host, remote, pinned); err != nil {
		t.Errorf("matching pinned key rejected: %v", err)
	}
	if err := cb(host, remote, testPub(t)); err == nil {
		t.Error("changed host key accepted, want rejection")
	}
}

func TestConfirmHost(t *testing.T) {
	key := testPub(t)
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
			got := confirmHost(strings.NewReader(tt.input), &out, tt.isTTY, "example.com:22", key)
			if got != tt.want {
				t.Errorf("confirmHost = %v, want %v", got, tt.want)
			}
			if !tt.isTTY && !strings.Contains(out.String(), "no terminal to confirm") {
				t.Errorf("no-TTY message missing: %q", out.String())
			}
			if tt.isTTY && !strings.Contains(out.String(), ssh.FingerprintSHA256(key)) {
				t.Errorf("prompt should show the fingerprint: %q", out.String())
			}
		})
	}
}

func TestAppendKnownHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	key := testPub(t)
	if err := appendKnownHost(path, "example.com:22", nil, key); err != nil {
		t.Fatalf("appendKnownHost: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	line := string(data)
	if !strings.Contains(line, "example.com") {
		t.Errorf("known_hosts missing hostname: %q", line)
	}
	if !strings.Contains(line, key.Type()) {
		t.Errorf("known_hosts missing key type %q: %q", key.Type(), line)
	}
}
