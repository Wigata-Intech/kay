package config

import (
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(st.Keys) != 0 || len(st.Servers) != 0 {
		t.Fatalf("expected empty store on first load")
	}
	if err := st.AddKey(Key{Name: "default", Type: KeyEd25519, Fingerprint: "SHA256:x"}); err != nil {
		t.Fatalf("add key: %v", err)
	}
	if err := st.AddServer(Server{Alias: "prod", Host: "10.0.0.1", Port: 22, User: "ubuntu", KeyName: "default"}); err != nil {
		t.Fatalf("add server: %v", err)
	}
	if err := st.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	st2, err := LoadFrom(dir)
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

func TestDuplicateAndMissing(t *testing.T) {
	st, _ := LoadFrom(t.TempDir())
	_ = st.AddKey(Key{Name: "k"})
	if err := st.AddKey(Key{Name: "k"}); err == nil {
		t.Error("expected duplicate key error")
	}
	if err := st.AddServer(Server{Alias: "a", KeyName: "missing"}); err == nil {
		t.Error("expected unknown-key error")
	}
	if err := st.AddServer(Server{Alias: "a", KeyName: "k"}); err != nil {
		t.Errorf("valid add failed: %v", err)
	}
	if err := st.AddServer(Server{Alias: "a", KeyName: "k"}); err == nil {
		t.Error("expected duplicate alias error")
	}
	if err := st.RemoveServer("a"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := st.RemoveServer("a"); err == nil {
		t.Error("expected missing-server error on second remove")
	}
}
