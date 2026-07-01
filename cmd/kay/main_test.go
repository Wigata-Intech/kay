// White-box (package main): these tests cover unexported CLI helpers. The
// interactive/os.Exit paths (main, the flag subcommands, stdin prompts) need a
// real terminal and are left to end-to-end use.
package main

import (
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"
)

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
