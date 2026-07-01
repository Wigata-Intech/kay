package config_test

import (
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
