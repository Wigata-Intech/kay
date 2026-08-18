// White-box (package main): the subcommand handlers are unexported, so tests
// must live in this package to drive them directly.
package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	sshx "github.com/Wigata-Intech/w-tools/x/sshx"

	"github.com/Wigata-Intech/kay/internal/dashboard"
	"github.com/Wigata-Intech/kay/internal/fleet"

	"github.com/Wigata-Intech/kay/internal/config"

	"golang.org/x/crypto/ssh/knownhosts"
)

var errNoTTY = errors.New("no tty")

// captureStdout redirects os.Stdout to a pipe while fn runs and returns what
// it printed. Restoration happens in the calling goroutine, so overriding
// tests must not run in parallel.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = old
		_ = r.Close()
		_ = w.Close()
	}()
	fn()
	os.Stdout = old
	_ = w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(data)
}

// noConfigDir clears every path config.Load can resolve so it must fail.
func noConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("KAY_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
}

// saveStore persists a seeded store so the handlers' own config.Load sees it.
func saveStore(t *testing.T, st *config.Store) {
	t.Helper()
	if err := st.Save(); err != nil {
		t.Fatalf("save store: %v", err)
	}
}

// pinHostKey pre-seeds kay's known_hosts with the test server's host key so
// TOFU passes without a terminal confirmation.
func pinHostKey(t *testing.T, st *config.Store, server *testSSHServer) {
	t.Helper()
	line := knownhosts.Line([]string{server.addr()}, server.hostSigner.PublicKey())
	if err := os.WriteFile(st.KnownHostsPath(), []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
}

// lockDir makes dir non-writable so saves and known_hosts creation fail,
// restoring the mode on cleanup so TempDir removal works.
func lockDir(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod cannot block writes for root")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

// setReadPassword overrides the readPassword seam for one test.
func setReadPassword(t *testing.T, fn func(int) ([]byte, error)) {
	t.Helper()
	old := readPassword
	t.Cleanup(func() { readPassword = old })
	readPassword = fn
}

// setIsTerminal overrides the isTerminal seam for one test.
func setIsTerminal(t *testing.T, v bool) {
	t.Helper()
	old := isTerminal
	t.Cleanup(func() { isTerminal = old })
	isTerminal = func(int) bool { return v }
}

// setVersion overrides the build-stamp variables for one test.
func setVersion(t *testing.T, v, c, d string) {
	t.Helper()
	oldV, oldC, oldD := version, commit, date
	t.Cleanup(func() { version, commit, date = oldV, oldC, oldD })
	version, commit, date = v, c, d
}

func TestCmdKeyGen(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())

	if err := cmdKey([]string{"gen", "--name", "x"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}

	st, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := st.FindKey("x"); err != nil {
		t.Errorf("expected key %q in store: %v", "x", err)
	}
}

func TestCmdKeyLs(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "x"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	if err := cmdKey([]string{"ls"}); err != nil {
		t.Errorf("cmdKey ls: %v", err)
	}
}

func TestCmdKeyShow(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "x"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	if err := cmdKey([]string{"show", "--name", "x"}); err != nil {
		t.Errorf("cmdKey show: %v", err)
	}
}

func TestCmdKeyErrors(t *testing.T) {
	tests := []struct {
		name   string
		inputs []string
	}{
		{"no subcommand", []string{}},
		{"unknown subcommand", []string{"bogus"}},
		{"gen missing name", []string{"gen"}},
		{"gen bad flag", []string{"gen", "--nope"}},
		{"gen invalid type", []string{"gen", "--name", "x", "--type", "dsa"}},
		{"show unknown name", []string{"show", "--name", "missing"}},
		{"show bad flag", []string{"show", "--nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KAY_HOME", t.TempDir())
			if err := cmdKey(tt.inputs); err == nil {
				t.Errorf("cmdKey(%v) = nil, want error", tt.inputs)
			}
		})
	}
}

func TestCmdKeyLsAnonymize(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "x"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	t.Setenv("KAY_DEMO", "1")
	out := captureStdout(t, func() {
		if err := cmdKey([]string{"ls"}); err != nil {
			t.Errorf("cmdKey ls: %v", err)
		}
	})
	if !strings.Contains(out, "key-1") || !strings.Contains(out, "SHA256:…") {
		t.Errorf("anonymized listing %q should mask name and fingerprint", out)
	}
}

func TestCmdKeyGenExistingFile(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "x"}); err != nil {
		t.Fatalf("first gen: %v", err)
	}
	if err := cmdKey([]string{"gen", "--name", "x"}); err == nil {
		t.Error("second gen = nil, want existing key file error")
	}
}

func TestCmdKeyGenDuplicateStoreEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KAY_HOME", home)
	if err := cmdKey([]string{"gen", "--name", "x"}); err != nil {
		t.Fatalf("first gen: %v", err)
	}
	// Removing the private file frees the path but leaves the store entry, so
	// the next gen writes fine and must fail on the duplicate name instead.
	if err := os.Remove(filepath.Join(home, "keys", "x")); err != nil {
		t.Fatalf("remove key file: %v", err)
	}
	err := cmdKey([]string{"gen", "--name", "x"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate gen = %v, want already-exists error", err)
	}
}

func TestCmdKeyGenSaveError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KAY_HOME", home)
	if _, err := config.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	lockDir(t, home)
	if err := cmdKey([]string{"gen", "--name", "x"}); err == nil {
		t.Error("cmdKey gen = nil, want save failure")
	}
}

func TestCmdKeyShowUnreadablePublic(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KAY_HOME", home)
	if err := cmdKey([]string{"gen", "--name", "x"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	if err := os.Remove(filepath.Join(home, "keys", "x.pub")); err != nil {
		t.Fatalf("remove pub: %v", err)
	}
	if err := cmdKey([]string{"show", "--name", "x"}); err == nil {
		t.Error("cmdKey show = nil, want read failure")
	}
}

func TestCmdServerAdd(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "k"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}

	if err := cmdServer([]string{"add", "--alias", "a", "--host", "h", "--user", "u", "--key", "k"}); err != nil {
		t.Fatalf("cmdServer add: %v", err)
	}

	st, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := st.FindServer("a"); err != nil {
		t.Errorf("expected server %q in store: %v", "a", err)
	}
}

func TestCmdServerLs(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdServer([]string{"ls"}); err != nil {
		t.Errorf("cmdServer ls: %v", err)
	}
}

func TestCmdServerRm(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "k"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	if err := cmdServer([]string{"add", "--alias", "a", "--host", "h", "--user", "u", "--key", "k"}); err != nil {
		t.Fatalf("cmdServer add: %v", err)
	}

	if err := cmdServer([]string{"rm", "--alias", "a"}); err != nil {
		t.Fatalf("cmdServer rm: %v", err)
	}

	st, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := st.FindServer("a"); err == nil {
		t.Errorf("server %q still present after rm", "a")
	}
}

func TestCmdServerErrors(t *testing.T) {
	tests := []struct {
		name   string
		inputs []string
	}{
		{"no subcommand", []string{}},
		{"unknown subcommand", []string{"bogus"}},
		{"add missing flags", []string{"add", "--alias", "a"}},
		{"add bad flag", []string{"add", "--nope"}},
		{"rm unknown alias", []string{"rm", "--alias", "missing"}},
		{"rm bad flag", []string{"rm", "--nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KAY_HOME", t.TempDir())
			if err := cmdServer(tt.inputs); err == nil {
				t.Errorf("cmdServer(%v) = nil, want error", tt.inputs)
			}
		})
	}
}

func TestCmdServerAddDuplicate(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "k"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	add := []string{"add", "--alias", "a", "--host", "h", "--user", "u", "--key", "k"}
	if err := cmdServer(add); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := cmdServer(add); err == nil {
		t.Error("duplicate add = nil, want error")
	}
}

func TestCmdServerLsAnonymize(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "k"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	if err := cmdServer([]string{"add", "--alias", "a", "--host", "h", "--user", "u", "--key", "k"}); err != nil {
		t.Fatalf("cmdServer add: %v", err)
	}
	t.Setenv("KAY_DEMO", "1")
	out := captureStdout(t, func() {
		if err := cmdServer([]string{"ls"}); err != nil {
			t.Errorf("cmdServer ls: %v", err)
		}
	})
	if !strings.Contains(out, "server-1") || !strings.Contains(out, "demo.host") {
		t.Errorf("anonymized listing %q should mask alias and host", out)
	}
}

func TestCmdServerAddSaveError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KAY_HOME", home)
	if err := cmdKey([]string{"gen", "--name", "k"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	lockDir(t, home)
	if err := cmdServer([]string{"add", "--alias", "a", "--host", "h", "--user", "u", "--key", "k"}); err == nil {
		t.Error("cmdServer add = nil, want save failure")
	}
}

func TestCmdServerRmSaveError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KAY_HOME", home)
	if err := cmdKey([]string{"gen", "--name", "k"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	if err := cmdServer([]string{"add", "--alias", "a", "--host", "h", "--user", "u", "--key", "k"}); err != nil {
		t.Fatalf("cmdServer add: %v", err)
	}
	lockDir(t, home)
	if err := cmdServer([]string{"rm", "--alias", "a"}); err == nil {
		t.Error("cmdServer rm = nil, want save failure")
	}
}

func TestCmdInstallNoPush(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "k"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	if err := cmdServer([]string{"add", "--alias", "a", "--host", "h", "--user", "u", "--key", "k"}); err != nil {
		t.Fatalf("cmdServer add: %v", err)
	}

	if err := cmdInstall([]string{"--alias", "a"}); err != nil {
		t.Errorf("cmdInstall: %v", err)
	}
}

func TestCmdInstallPush(t *testing.T) {
	setup := func(t *testing.T, opts testServerOpts) (*config.Store, *testSSHServer) {
		t.Helper()
		st, _ := newKeyedStore(t)
		server := newTestSSHServer(t, opts)
		addServer(t, st, "web", server.addr())
		saveStore(t, st)
		return st, server
	}

	t.Run("pushes the key over a password login", func(t *testing.T) {
		st, server := setup(t, testServerOpts{password: "hunter2"})
		pinHostKey(t, st, server)
		setReadPassword(t, func(int) ([]byte, error) { return []byte("hunter2"), nil })
		out := captureStdout(t, func() {
			if err := cmdInstall([]string{"--alias", "web", "--push"}); err != nil {
				t.Errorf("cmdInstall(--push) error = %v", err)
			}
		})
		if !strings.Contains(out, "installed key") {
			t.Errorf("output %q missing install confirmation", out)
		}
		recs := server.execRecords()
		if len(recs) != 1 || !strings.Contains(recs[0], ">> ~/.ssh/authorized_keys") {
			t.Errorf("exec records = %v, want one authorized_keys append", recs)
		}
	})

	t.Run("password read failure aborts", func(t *testing.T) {
		st, server := setup(t, testServerOpts{password: "hunter2"})
		pinHostKey(t, st, server)
		setReadPassword(t, func(int) ([]byte, error) { return nil, errNoTTY })
		if err := cmdInstall([]string{"--alias", "web", "--push"}); !errors.Is(err, errNoTTY) {
			t.Errorf("cmdInstall(--push) error = %v, want errNoTTY", err)
		}
	})

	t.Run("unusable known_hosts fails the policy", func(t *testing.T) {
		st, _ := setup(t, testServerOpts{password: "hunter2"})
		setReadPassword(t, func(int) ([]byte, error) { return []byte("hunter2"), nil })
		lockDir(t, st.Dir())
		if err := cmdInstall([]string{"--alias", "web", "--push"}); err == nil {
			t.Error("cmdInstall(--push) error = nil, want host-key policy failure")
		}
	})

	t.Run("wrong password carries the hint", func(t *testing.T) {
		st, server := setup(t, testServerOpts{password: "hunter2"})
		pinHostKey(t, st, server)
		setReadPassword(t, func(int) ([]byte, error) { return []byte("nope"), nil })
		err := cmdInstall([]string{"--alias", "web", "--push"})
		if err == nil || !strings.Contains(err.Error(), "wrong password?") {
			t.Errorf("cmdInstall(--push) error = %v, want the wrong-password hint", err)
		}
	})

	t.Run("remote command failure reports install failed", func(t *testing.T) {
		st, server := setup(t, testServerOpts{password: "hunter2", execExit: 1})
		pinHostKey(t, st, server)
		setReadPassword(t, func(int) ([]byte, error) { return []byte("hunter2"), nil })
		err := cmdInstall([]string{"--alias", "web", "--push"})
		if err == nil || !strings.Contains(err.Error(), "install failed") {
			t.Errorf("cmdInstall(--push) error = %v, want install failure", err)
		}
	})
}

func TestCmdInstallErrors(t *testing.T) {
	t.Run("bad flag", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		if err := cmdInstall([]string{"--nope"}); err == nil {
			t.Error("cmdInstall(--nope) error = nil, want flag error")
		}
	})

	t.Run("no servers to pick", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		if err := cmdInstall(nil); err == nil {
			t.Error("cmdInstall() error = nil, want no-servers failure")
		}
	})

	t.Run("server references missing key", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		addServer(t, st, "web", "127.0.0.1:22")
		st.Keys = nil
		saveStore(t, st)
		if err := cmdInstall([]string{"--alias", "web"}); err == nil {
			t.Error("cmdInstall() error = nil, want missing-key failure")
		}
	})

	t.Run("unreadable public key", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		addServer(t, st, "web", "127.0.0.1:22")
		saveStore(t, st)
		if err := os.Remove(filepath.Join(st.KeysDir(), "id.pub")); err != nil {
			t.Fatalf("remove pub: %v", err)
		}
		if err := cmdInstall([]string{"--alias", "web"}); err == nil {
			t.Error("cmdInstall() error = nil, want read failure")
		}
	})
}

func TestCmdConnect(t *testing.T) {
	t.Run("connects and runs the remote shell", func(t *testing.T) {
		st, pub := newKeyedStore(t)
		server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
		addServer(t, st, "web", server.addr())
		saveStore(t, st)
		out := captureStdout(t, func() {
			if err := cmdConnect([]string{"--alias", "web", "--insecure"}); err != nil {
				t.Errorf("cmdConnect() error = %v", err)
			}
		})
		if !strings.Contains(out, "connected to tester@") {
			t.Errorf("output %q missing connect banner", out)
		}
	})

	t.Run("bad flag", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		if err := cmdConnect([]string{"--nope"}); err == nil {
			t.Error("cmdConnect(--nope) error = nil, want flag error")
		}
	})

	t.Run("no servers to pick", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		if err := cmdConnect(nil); err == nil {
			t.Error("cmdConnect() error = nil, want no-servers failure")
		}
	})

	t.Run("dial failure", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		addServer(t, st, "web", "127.0.0.1:1")
		saveStore(t, st)
		if err := cmdConnect([]string{"--alias", "web", "--insecure"}); err == nil {
			t.Error("cmdConnect() error = nil, want dial failure")
		}
	})
}

func TestCmdExec(t *testing.T) {
	setup := func(t *testing.T, opts testServerOpts) {
		t.Helper()
		st, pub := newKeyedStore(t)
		opts.authorizedKey = pub
		server := newTestSSHServer(t, opts)
		addServer(t, st, "web", server.addr())
		saveStore(t, st)
	}

	t.Run("prints output and adds the missing newline", func(t *testing.T) {
		setup(t, testServerOpts{})
		var err error
		out := captureStdout(t, func() {
			err = cmdExec([]string{"--alias", "web", "--insecure", "uptime"})
		})
		if err != nil {
			t.Fatalf("cmdExec() error = %v", err)
		}
		if out != "ok:uptime\n" {
			t.Errorf("cmdExec() printed %q, want %q", out, "ok:uptime\n")
		}
	})

	t.Run("keeps an existing trailing newline", func(t *testing.T) {
		setup(t, testServerOpts{})
		var err error
		out := captureStdout(t, func() {
			err = cmdExec([]string{"--alias", "web", "--insecure", "uptime\n"})
		})
		if err != nil {
			t.Fatalf("cmdExec() error = %v", err)
		}
		if out != "ok:uptime\n" {
			t.Errorf("cmdExec() printed %q, want %q", out, "ok:uptime\n")
		}
	})

	t.Run("remote failure still prints the output", func(t *testing.T) {
		setup(t, testServerOpts{execExit: 1})
		var err error
		out := captureStdout(t, func() {
			err = cmdExec([]string{"--alias", "web", "--insecure", "false"})
		})
		if err == nil {
			t.Error("cmdExec() error = nil, want remote exit failure")
		}
		if out != "ok:false\n" {
			t.Errorf("cmdExec() printed %q, want %q", out, "ok:false\n")
		}
	})

	t.Run("missing command", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		err := cmdExec([]string{"--insecure"})
		if err == nil || !strings.Contains(err.Error(), "no command given") {
			t.Errorf("cmdExec() error = %v, want no-command failure", err)
		}
	})

	t.Run("bad flag", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		if err := cmdExec([]string{"--nope"}); err == nil {
			t.Error("cmdExec(--nope) error = nil, want flag error")
		}
	})

	t.Run("no servers to pick", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		if err := cmdExec([]string{"uptime"}); err == nil {
			t.Error("cmdExec() error = nil, want no-servers failure")
		}
	})

	t.Run("dial failure", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		addServer(t, st, "web", "127.0.0.1:1")
		saveStore(t, st)
		if err := cmdExec([]string{"--alias", "web", "--insecure", "uptime"}); err == nil {
			t.Error("cmdExec() error = nil, want dial failure")
		}
	})
}

func TestCmdDashboard(t *testing.T) {
	t.Run("bad flag", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		if err := cmdDashboard([]string{"--nope"}); err == nil {
			t.Error("cmdDashboard(--nope) error = nil, want flag error")
		}
	})

	t.Run("no servers to pick", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		if err := cmdDashboard(nil); err == nil {
			t.Error("cmdDashboard() error = nil, want no-servers failure")
		}
	})

	t.Run("dial failure", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		addServer(t, st, "web", "127.0.0.1:1")
		saveStore(t, st)
		if err := cmdDashboard([]string{"--alias", "web", "--insecure"}); err == nil {
			t.Error("cmdDashboard() error = nil, want dial failure")
		}
	})
}

func TestCmdFleet(t *testing.T) {
	t.Run("plain mode with no servers reports the fleet error", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		setIsTerminal(t, false)
		err := cmdFleet(nil)
		if err == nil || !strings.Contains(err.Error(), "no servers registered") {
			t.Errorf("cmdFleet() error = %v, want no-servers failure", err)
		}
	})

	t.Run("interactive mode fails without a real terminal", func(t *testing.T) {
		st, _ := newKeyedStore(t)
		addServer(t, st, "web", "127.0.0.1:22")
		saveStore(t, st)
		setIsTerminal(t, true)
		if err := cmdFleet([]string{"--insecure"}); err == nil {
			t.Error("cmdFleet() error = nil, want screen setup failure")
		}
	})

	t.Run("bad flag", func(t *testing.T) {
		t.Setenv("KAY_HOME", t.TempDir())
		if err := cmdFleet([]string{"--nope"}); err == nil {
			t.Error("cmdFleet(--nope) error = nil, want flag error")
		}
	})

	t.Run("unusable known_hosts fails the policy", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("KAY_HOME", home)
		if _, err := config.Load(); err != nil {
			t.Fatalf("load: %v", err)
		}
		lockDir(t, home)
		if err := cmdFleet(nil); err == nil {
			t.Error("cmdFleet() error = nil, want host-key policy failure")
		}
	})
}

func TestOverviewLayoutOpts(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	st, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	panels, save := overviewLayoutOpts(st)
	if panels != nil {
		t.Errorf("initial layout = %v, want nil (uncustomised)", panels)
	}
	want := []config.PanelPref{{Name: "cpu"}, {Name: "mem", Hidden: true}}
	if err := save(want); err != nil {
		t.Fatalf("save layout: %v", err)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.OverviewPanels(); !reflect.DeepEqual(got, want) {
		t.Errorf("persisted layout = %v, want %v", got, want)
	}
}

func TestHandlersLoadError(t *testing.T) {
	tests := []struct {
		name string
		h    handler
		args []string
	}{
		{name: "key", h: cmdKey, args: []string{"ls"}},
		{name: "server", h: cmdServer, args: []string{"ls"}},
		{name: "install", h: cmdInstall, args: nil},
		{name: "connect", h: cmdConnect, args: nil},
		{name: "exec", h: cmdExec, args: []string{"uptime"}},
		{name: "dashboard", h: cmdDashboard, args: nil},
		{name: "fleet", h: cmdFleet, args: nil},
		{name: "ls", h: cmdLs, args: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noConfigDir(t)
			if err := tt.h(tt.args); err == nil {
				t.Errorf("%s handler error = nil, want config load failure", tt.name)
			}
		})
	}
}

func TestCmdLs(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "k"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	if err := cmdServer([]string{"add", "--alias", "a", "--host", "h", "--user", "u", "--key", "k"}); err != nil {
		t.Fatalf("cmdServer add: %v", err)
	}
	if err := cmdLs(nil); err != nil {
		t.Errorf("cmdLs: %v", err)
	}
}

func TestCmdLsAnonymize(t *testing.T) {
	t.Setenv("KAY_HOME", t.TempDir())
	if err := cmdKey([]string{"gen", "--name", "k"}); err != nil {
		t.Fatalf("cmdKey gen: %v", err)
	}
	if err := cmdServer([]string{"add", "--alias", "a", "--host", "h", "--user", "u", "--key", "k"}); err != nil {
		t.Fatalf("cmdServer add: %v", err)
	}
	t.Setenv("KAY_DEMO", "1")
	out := captureStdout(t, func() {
		if err := cmdLs(nil); err != nil {
			t.Errorf("cmdLs: %v", err)
		}
	})
	for _, want := range []string{"<config dir>", "key-1", "server-1", "demo.host"} {
		if !strings.Contains(out, want) {
			t.Errorf("anonymized overview %q missing %q", out, want)
		}
	}
}

func TestCmdVersion(t *testing.T) {
	// go test never stamps vcs.* build settings into the test binary, so the
	// fallback lookup deterministically finds nothing here.
	tests := []struct {
		name    string
		version string
		commit  string
		date    string
		want    string
	}{
		{name: "full ldflags stamp", version: "1.2.3", commit: "abc1234", date: "2026-01-02", want: "kay 1.2.3 (abc1234, 2026-01-02)\n"},
		{name: "commit only", version: "1.2.3", commit: "abc1234", date: "", want: "kay 1.2.3 (abc1234)\n"},
		{name: "date only", version: "1.2.3", commit: "", date: "2026-01-02", want: "kay 1.2.3 (2026-01-02)\n"},
		{name: "dev build without stamp", version: "dev", commit: "", date: "", want: "kay dev\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setVersion(t, tt.version, tt.commit, tt.date)
			if got := captureStdout(t, cmdVersion); got != tt.want {
				t.Errorf("cmdVersion() printed %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStampFrom(t *testing.T) {
	info := func(settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Settings: settings}
	}
	set := func(k, v string) debug.BuildSetting { return debug.BuildSetting{Key: k, Value: v} }
	tests := []struct {
		name     string
		bi       *debug.BuildInfo
		wantRev  string
		wantWhen string
		wantOK   bool
	}{
		{
			name:     "long revision truncated and marked dirty",
			bi:       info(set("vcs.revision", "0123456789abcdef0123"), set("vcs.time", "2026-01-02T03:04:05Z"), set("vcs.modified", "true")),
			wantRev:  "0123456789ab-dirty",
			wantWhen: "2026-01-02T03:04:05Z",
			wantOK:   true,
		},
		{
			name:    "short clean revision kept as-is",
			bi:      info(set("vcs.revision", "abc123"), set("vcs.modified", "false")),
			wantRev: "abc123",
			wantOK:  true,
		},
		{
			name:     "time only still stamps",
			bi:       info(set("vcs.time", "2026-01-02T03:04:05Z")),
			wantWhen: "2026-01-02T03:04:05Z",
			wantOK:   true,
		},
		{name: "modified without revision is no stamp", bi: info(set("vcs.modified", "true"))},
		{name: "no vcs settings", bi: info()},
		{name: "nil info", bi: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rev, when, ok := stampFrom(tt.bi)
			if rev != tt.wantRev || when != tt.wantWhen || ok != tt.wantOK {
				t.Errorf("stampFrom() = (%q, %q, %v), want (%q, %q, %v)",
					rev, when, ok, tt.wantRev, tt.wantWhen, tt.wantOK)
			}
		})
	}
}

func TestUsage(t *testing.T) {
	out := captureStdout(t, usage)
	if !strings.Contains(out, "kay - manage Linux servers over SSH") {
		t.Errorf("usage() output %q missing the header", out)
	}
}

func TestCmdVersionVCSFallback(t *testing.T) {
	prev := vcsStamp
	vcsStamp = func() (string, string, bool) { return "abc123def456", "2026-08-15T00:00:00Z", true }
	t.Cleanup(func() { vcsStamp = prev })
	setVersion(t, "dev", "", "")
	out := captureStdout(t, func() { cmdVersion() })
	if want := "kay dev (abc123def456, 2026-08-15T00:00:00Z)\n"; out != want {
		t.Errorf("cmdVersion output = %q, want %q", out, want)
	}
	// The default stamp reads the test binary's build info: no vcs settings,
	// so it reports not-ok — executing the seam's own body either way.
	if _, _, ok := prev(); ok {
		t.Error("default vcsStamp ok = true, want false in a test binary")
	}
}

func TestCmdDashboardRun(t *testing.T) {
	st, pub := newKeyedStore(t)
	server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
	_ = addServer(t, st, "test", server.addr())

	if err := st.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	var gotRedial bool
	prev := dashboardRun
	dashboardRun = func(client dashboard.Client, srv config.Server, opts dashboard.Options) error {
		if client == nil {
			t.Error("dashboardRun client = nil")
		}
		c, err := opts.Redial()
		if err != nil {
			t.Errorf("Redial() error = %v", err)
		} else if rc, ok := c.(runner); ok {
			_ = rc.c.Close() // the stub owns this dial; leaking it wedges server cleanup
		} else {
			t.Errorf("Redial() client = %T, want runner", c)
		}
		gotRedial = true
		// With the key file gone, the redial path must surface its error.
		if err := os.Remove(st.Keys[0].PrivatePath); err != nil {
			t.Fatalf("remove key: %v", err)
		}
		if _, rerr := opts.Redial(); rerr == nil {
			t.Error("Redial() without the key file error = nil, want dial failure")
		}
		return nil
	}
	t.Cleanup(func() { dashboardRun = prev })

	if err := cmdDashboard([]string{"--alias", "test", "--insecure"}); err != nil {
		t.Fatalf("cmdDashboard() error = %v", err)
	}
	if !gotRedial {
		t.Error("Redial closure never exercised")
	}
}

func TestCmdFleetRun(t *testing.T) {
	st, pub := newKeyedStore(t)
	server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
	_ = addServer(t, st, "test", server.addr())
	if err := st.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	setIsTerminal(t, false)

	prev := fleetRun
	fleetRun = func(hosts []fleet.Host, opts fleet.Options) error {
		if len(hosts) != 1 {
			t.Fatalf("hosts = %d, want 1", len(hosts))
		}
		c, err := hosts[0].Dial(context.Background())
		if err != nil {
			t.Errorf("Dial() error = %v", err)
		} else {
			_ = c.Close()
		}
		return nil
	}
	t.Cleanup(func() { fleetRun = prev })

	if err := cmdFleet([]string{"--insecure"}); err != nil {
		t.Fatalf("cmdFleet() error = %v", err)
	}
}

// ---- fleetDrill: the interactive fleet→dashboard loop, driven headless ----

// fakeScreen satisfies uiScreen (and the fleet/dashboard Screen interfaces)
// with a fixed size and discarded output.
type fakeScreen struct {
	mu           sync.Mutex
	draws        int
	sawDashboard bool
	lines        []string
}

func (f *fakeScreen) Size() (int, int) { return 100, 30 }
func (f *fakeScreen) Draw(lines []string) {
	f.mu.Lock()
	f.draws++
	f.lines = append(f.lines, lines...)
	for _, l := range lines {
		if strings.Contains(l, "Overview") {
			f.sawDashboard = true // the tab bar renders only inside a dashboard
		}
	}
	f.mu.Unlock()
}
func (f *fakeScreen) Close() {}

func (f *fakeScreen) dashboardSeen() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sawDashboard
}

// saw reports whether any drawn line contained s.
func (f *fakeScreen) saw(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.lines {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

type stdinStep struct {
	delay time.Duration
	keys  string
}

// scriptStdin replaces stdinFile with a pipe and writes each step's bytes
// after its delay, letting tests time keystrokes against session state.
func scriptStdin(t *testing.T, steps []stdinStep) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := stdinFile
	stdinFile = r
	t.Cleanup(func() {
		stdinFile = prev
		_ = w.Close()
		_ = r.Close()
	})
	go func() {
		for _, st := range steps {
			time.Sleep(st.delay)
			if _, err := w.Write([]byte(st.keys)); err != nil {
				return
			}
		}
	}()
	return w
}

// drillFixture: a one-host fleet backed by the in-process server, the screen
// seam swapped for a recording fake, and a store for the layout options.
func drillFixture(t *testing.T) (*config.Store, []fleet.Host, fleet.Options, *fakeScreen, *testSSHServer) {
	t.Helper()
	st, pub := newKeyedStore(t)
	server := newTestSSHServer(t, testServerOpts{authorizedKey: pub})
	srv := addServer(t, st, "test", server.addr())

	scr := &fakeScreen{}
	prevScreen := newScreen
	newScreen = func() (uiScreen, error) { return scr, nil }
	t.Cleanup(func() { newScreen = prevScreen })

	hostKey := server.hostKey()
	hosts := []fleet.Host{{Server: *srv, Dial: func(ctx context.Context) (*sshx.Client, error) {
		return dialWith(ctx, st, srv, hostKey)
	}}}
	fopts := fleet.Options{Interval: 100 * time.Millisecond, Color: "never"}
	return st, hosts, fopts, scr, server
}

// drillKeys writes Enter once the host has served a metrics round (implying
// the pooled connection is Ready, so Enter deterministically drills in), then
// the remaining key groups at fixed beats.
func drillKeys(w *os.File, server *testSSHServer, rest ...string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(server.execRecords()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	_, _ = w.Write([]byte("\r"))
	for _, keys := range rest {
		time.Sleep(400 * time.Millisecond)
		_, _ = w.Write([]byte(keys))
	}
}

func TestFleetDrill(t *testing.T) {
	t.Run("quit at the fleet overview", func(t *testing.T) {
		st, hosts, fopts, scr, _ := drillFixture(t)
		scriptStdin(t, []stdinStep{{delay: 50 * time.Millisecond, keys: "q"}})
		if err := fleetDrill(st, hosts, fopts, false); err != nil {
			t.Errorf("fleetDrill() error = %v", err)
		}
		if scr.dashboardSeen() {
			t.Error("dashboard drawn without a drill-in")
		}
	})

	t.Run("drill in then exit the app from the dashboard", func(t *testing.T) {
		st, hosts, fopts, scr, server := drillFixture(t)
		w := scriptStdin(t, nil)
		go drillKeys(w, server, "\x03") // Ctrl-C inside the dashboard exits the app
		if err := fleetDrill(st, hosts, fopts, false); err != nil {
			t.Errorf("fleetDrill() error = %v", err)
		}
		if !scr.dashboardSeen() {
			t.Error("dashboard never drawn: the drill-in did not happen")
		}
	})

	t.Run("drill in, back to fleet, then quit", func(t *testing.T) {
		st, hosts, fopts, scr, server := drillFixture(t)
		w := scriptStdin(t, nil)
		go drillKeys(w, server, "q", "q") // back to the fleet, then quit it
		if err := fleetDrill(st, hosts, fopts, false); err != nil {
			t.Errorf("fleetDrill() error = %v", err)
		}
		if !scr.dashboardSeen() {
			t.Error("dashboard never drawn: the drill-in did not happen")
		}
	})

	t.Run("screen failure returns the error", func(t *testing.T) {
		st, hosts, fopts, _, _ := drillFixture(t)
		prev := newScreen
		newScreen = func() (uiScreen, error) { return nil, errSizeFailed }
		t.Cleanup(func() { newScreen = prev })
		if err := fleetDrill(st, hosts, fopts, false); err == nil {
			t.Error("fleetDrill() error = nil, want screen failure")
		}
	})
}
