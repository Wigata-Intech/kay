// White-box (package main): the subcommand handlers are unexported, so tests
// must live in this package to drive them directly.
package main

import (
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"
)

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
		{"show unknown name", []string{"show", "--name", "missing"}},
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

func TestCmdVersion(t *testing.T) {
	cmdVersion() // prints; assert no panic
}

func TestUsage(t *testing.T) {
	usage() // prints; assert no panic
}
