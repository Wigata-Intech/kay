package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"
)

// TestStoreRoundTrip is the happy path: load an empty store, add a key and a
// server, save, reload, and read them back. The steps share state, so this is
// an ordered sequence rather than a table.
func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(st.Keys) != 0 || len(st.Servers) != 0 {
		t.Fatalf("expected empty store on first load")
	}
	if err := st.AddKey(config.Key{Name: "default", Type: config.KeyEd25519, Fingerprint: "SHA256:x"}); err != nil {
		t.Fatalf("add key: %v", err)
	}
	if err := st.AddServer(config.Server{Alias: "prod", Host: "10.0.0.1", Port: 22, User: "ubuntu", KeyName: "default"}); err != nil {
		t.Fatalf("add server: %v", err)
	}
	if err := st.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	st2, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(st2.Keys) != 1 || len(st2.Servers) != 1 {
		t.Fatalf("expected 1 key and 1 server, got %d/%d", len(st2.Keys), len(st2.Servers))
	}
	srv, err := st2.FindServer("prod")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if srv.Addr() != "10.0.0.1:22" {
		t.Errorf("Addr() = %q", srv.Addr())
	}
}

// TestStoreValidation drives add/remove against one shared store. Ordering is
// dictated by store state (duplicate/missing checks need a prior valid entry),
// so the positive steps that establish state come first, then the error cases,
// then the final remove sequence.
func TestStoreValidation(t *testing.T) {
	st, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	steps := []struct {
		name    string
		op      func() error
		wantErr bool
	}{
		// positive: establish a key and a server
		{"add key", func() error { return st.AddKey(config.Key{Name: "k"}) }, false},
		{"add server with valid key", func() error {
			return st.AddServer(config.Server{Alias: "a", KeyName: "k"})
		}, false},
		// error: duplicates and dangling references are rejected
		{"duplicate key", func() error { return st.AddKey(config.Key{Name: "k"}) }, true},
		{"server references missing key", func() error {
			return st.AddServer(config.Server{Alias: "b", KeyName: "missing"})
		}, true},
		{"duplicate alias", func() error {
			return st.AddServer(config.Server{Alias: "a", KeyName: "k"})
		}, true},
		// positive then error: remove the server, then removing again fails
		{"remove server", func() error { return st.RemoveServer("a") }, false},
		{"remove missing server", func() error { return st.RemoveServer("a") }, true},
	}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.op()
			if tt.wantErr && err == nil {
				t.Errorf("%s: got nil error, want error", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("%s: unexpected error: %v", tt.name, err)
			}
		})
	}
}

// TestStorePathHelpers checks the resolved-path accessors. Load() is exercised
// via KAY_HOME (set with t.Setenv) so the real user config dir is never touched.
func TestStorePathHelpers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KAY_HOME", dir)

	st, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Dir", st.Dir(), dir},
		{"KeysDir", st.KeysDir(), filepath.Join(dir, "keys")},
		{"KnownHostsPath", st.KnownHostsPath(), filepath.Join(dir, "known_hosts")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

// TestServerAddr covers the host:port formatting for a non-default port.
func TestServerAddr(t *testing.T) {
	tests := []struct {
		name string
		srv  config.Server
		want string
	}{
		{"default port", config.Server{Host: "10.0.0.1", Port: 22}, "10.0.0.1:22"},
		{"custom port", config.Server{Host: "example.com", Port: 2222}, "example.com:2222"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.srv.Addr(); got != tt.want {
				t.Errorf("Addr() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLoadFromCorruptJSON verifies that an unparseable config.json is surfaced
// as an error rather than silently ignored. LoadFrom reads <dir>/config.json.
func TestLoadFromCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	// LoadFrom creates <dir>/keys itself, but the config file must exist and be
	// invalid before we call it, so pre-create the tree and write garbage.
	if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := config.LoadFrom(dir); err == nil {
		t.Fatalf("LoadFrom on corrupt JSON = nil error, want error")
	}
}

// TestFindNotFound covers the not-found error paths of the lookup helpers.
func TestFindNotFound(t *testing.T) {
	st, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if _, err := st.FindKey("nope"); err == nil {
		t.Errorf("FindKey(nope) = nil error, want error")
	}
	if _, err := st.FindServer("nope"); err == nil {
		t.Errorf("FindServer(nope) = nil error, want error")
	}
}

// TestKeyTypeValues pins the string values of the KeyType constants, which are
// serialised verbatim into the on-disk store.
func TestKeyTypeValues(t *testing.T) {
	if config.KeyEd25519 != "ed25519" {
		t.Errorf("KeyEd25519 = %q", config.KeyEd25519)
	}
	if config.KeyRSA != "rsa" {
		t.Errorf("KeyRSA = %q", config.KeyRSA)
	}
}
