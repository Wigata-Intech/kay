// White-box (package app): drives the server form/confirm views directly.
package app

import (
	"os"
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// submitForm drives a formView to its last field and submits.
func submitForm(v View) Action {
	f := v.(*formView)
	for f.form.Focus() < len(f.form.Fields)-1 {
		f.Handle(key(tui.KeyTab))
	}
	return f.Handle(key(tui.KeyEnter))
}

// setField overwrites one field's value on a formView.
func setField(v View, idx int, val string) {
	v.(*formView).form.Fields[idx].Input.SetValue(val)
}

func fieldError(v View, idx int) string {
	return v.(*formView).form.Fields[idx].Error
}

func TestParseServer(t *testing.T) {
	c := newTestConsole(t)
	addTestKey(t, c, "id")

	tests := []struct {
		name    string
		vals    []string
		wantErr map[int]string // field index -> message fragment; empty = valid
	}{
		{"valid", []string{"web", "10.0.0.1", "22", "u", "id"}, nil},
		{"whitespace trimmed", []string{" web ", " 10.0.0.1 ", " 22 ", " u ", " id "}, nil},
		{"missing required fields", []string{"", "", "22", "", "id"}, map[int]string{
			fldAlias: "required", fldHost: "required", fldUser: "required",
		}},
		{"bad port", []string{"web", "h", "eleventy", "u", "id"}, map[int]string{fldPort: "port number"}},
		{"port out of range", []string{"web", "h", "70000", "u", "id"}, map[int]string{fldPort: "port number"}},
		{"missing key", []string{"web", "h", "22", "u", ""}, map[int]string{fldKey: "required"}},
		{"unknown key", []string{"web", "h", "22", "u", "nope"}, map[int]string{fldKey: "no such key"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, errs := c.parseServer(tt.vals)
			if len(tt.wantErr) == 0 {
				if errs != nil || srv == nil {
					t.Fatalf("parseServer() = %+v, %q; want a server", srv, errs)
				}
				if srv.Alias != "web" || srv.Port != 22 {
					t.Errorf("parsed server = %+v, want trimmed values", srv)
				}
				return
			}
			if errs == nil {
				t.Fatal("parseServer() errs = nil, want field errors")
			}
			for idx, frag := range tt.wantErr {
				if !strings.Contains(errs[idx], frag) {
					t.Errorf("field %d error = %q, want %q", idx, errs[idx], frag)
				}
			}
		})
	}
}

func TestAddServerView(t *testing.T) {
	t.Run("valid submit stores, saves, and marks dirty", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		v := c.addServerView()
		setField(v, fldAlias, "web")
		setField(v, fldHost, "10.0.0.1")
		setField(v, fldUser, "u")
		// Port prefilled 22, Key prefilled with the only key.
		if act := submitForm(v); act.kind != actPop {
			t.Fatalf("submit action = %v, want pop", act.kind)
		}
		if _, err := c.store.FindServer("web"); err != nil {
			t.Errorf("server not stored: %v", err)
		}
		if !c.takeHostsDirty() {
			t.Error("hostsDirty not set")
		}
	})

	t.Run("duplicate alias surfaces the store error", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		v := c.addServerView()
		setField(v, fldAlias, "web")
		setField(v, fldHost, "h")
		setField(v, fldUser, "u")
		setField(v, fldKey, "id")
		if act := submitForm(v); act.kind != actNone {
			t.Fatalf("submit action = %v, want none", act.kind)
		}
		if status := v.(*formView).status; !strings.Contains(status, "already exists") {
			t.Errorf("status = %q, want the duplicate error", status)
		}
	})

	t.Run("save failure surfaces on the status line", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		lockStoreDir(t, c)
		v := c.addServerView()
		setField(v, fldAlias, "web")
		setField(v, fldHost, "h")
		setField(v, fldUser, "u")
		setField(v, fldKey, "id")
		if act := submitForm(v); act.kind != actNone {
			t.Fatalf("submit action = %v, want none", act.kind)
		}
		if v.(*formView).status == "" {
			t.Error("save failure not shown")
		}
	})

	t.Run("field errors keep the form open", func(t *testing.T) {
		c := newTestConsole(t)
		v := c.addServerView()
		if act := submitForm(v); act.kind != actNone {
			t.Fatalf("submit action = %v, want none", act.kind)
		}
		if got := fieldError(v, fldAlias); got != "required" {
			t.Errorf("alias error = %q, want required", got)
		}
	})
}

func TestEditServerView(t *testing.T) {
	newEditConsole := func(t *testing.T) *Console {
		t.Helper()
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		return c
	}

	t.Run("prefilled from the server", func(t *testing.T) {
		c := newEditConsole(t)
		v := c.editServerView(c.store.Servers[0]).(*formView)
		if got := v.form.Values(); got[fldAlias] != "web" || got[fldPort] != "22" || got[fldKey] != "id" {
			t.Errorf("prefill = %q, want the server's values", got)
		}
	})

	t.Run("edit updates in place and marks dirty", func(t *testing.T) {
		c := newEditConsole(t)
		v := c.editServerView(c.store.Servers[0])
		setField(v, fldHost, "10.9.9.9")
		if act := submitForm(v); act.kind != actPop {
			t.Fatalf("submit action = %v, want pop", act.kind)
		}
		if srv, _ := c.store.FindServer("web"); srv == nil || srv.Host != "10.9.9.9" {
			t.Errorf("server after edit = %+v, want the new host", srv)
		}
		if !c.takeHostsDirty() {
			t.Error("hostsDirty not set")
		}
	})

	t.Run("rename to a fresh alias works", func(t *testing.T) {
		c := newEditConsole(t)
		v := c.editServerView(c.store.Servers[0])
		setField(v, fldAlias, "web2")
		if act := submitForm(v); act.kind != actPop {
			t.Fatalf("submit action = %v, want pop", act.kind)
		}
		if _, err := c.store.FindServer("web2"); err != nil {
			t.Errorf("renamed server missing: %v", err)
		}
	})

	t.Run("rename onto an existing alias is refused", func(t *testing.T) {
		c := newEditConsole(t)
		addTestServer(t, c, "other")
		v := c.editServerView(c.store.Servers[0])
		setField(v, fldAlias, "other")
		if act := submitForm(v); act.kind != actNone {
			t.Fatalf("submit action = %v, want none", act.kind)
		}
		if got := fieldError(v, fldAlias); !strings.Contains(got, "already in use") {
			t.Errorf("alias error = %q, want already in use", got)
		}
	})

	t.Run("field errors keep the edit form open", func(t *testing.T) {
		c := newEditConsole(t)
		v := c.editServerView(c.store.Servers[0])
		setField(v, fldAlias, "")
		if act := submitForm(v); act.kind != actNone {
			t.Fatalf("submit action = %v, want none", act.kind)
		}
		if got := fieldError(v, fldAlias); got != "required" {
			t.Errorf("alias error = %q, want required", got)
		}
	})

	t.Run("server deleted underneath the form errors", func(t *testing.T) {
		c := newEditConsole(t)
		v := c.editServerView(c.store.Servers[0])
		if err := c.store.RemoveServer("web"); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if act := submitForm(v); act.kind != actNone {
			t.Fatalf("submit action = %v, want none", act.kind)
		}
		if v.(*formView).status == "" {
			t.Error("missing-server error not shown")
		}
	})

	t.Run("save failure surfaces on the status line", func(t *testing.T) {
		c := newEditConsole(t)
		v := c.editServerView(c.store.Servers[0])
		setField(v, fldHost, "10.9.9.9")
		lockStoreDir(t, c)
		if act := submitForm(v); act.kind != actNone {
			t.Fatalf("submit action = %v, want none", act.kind)
		}
		if v.(*formView).status == "" {
			t.Error("save failure not shown")
		}
	})
}

func TestDeleteServerView(t *testing.T) {
	t.Run("confirm removes and marks dirty", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		v := c.deleteServerView(c.store.Servers[0])
		v.Handle(rn('y'))
		if len(c.store.Servers) != 0 {
			t.Errorf("servers left = %d, want 0", len(c.store.Servers))
		}
		if !c.takeHostsDirty() {
			t.Error("hostsDirty not set")
		}
	})

	t.Run("decline leaves the server", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		v := c.deleteServerView(c.store.Servers[0])
		v.Handle(rn('n'))
		if len(c.store.Servers) != 1 {
			t.Errorf("servers left = %d, want 1", len(c.store.Servers))
		}
		if c.takeHostsDirty() {
			t.Error("hostsDirty set on decline")
		}
	})

	t.Run("server already gone surfaces on the console status", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		v := c.deleteServerView(c.store.Servers[0])
		if err := c.store.RemoveServer("web"); err != nil {
			t.Fatalf("remove: %v", err)
		}
		v.Handle(rn('y'))
		if c.status == "" {
			t.Error("missing-server error not shown")
		}
		if c.takeHostsDirty() {
			t.Error("hostsDirty set on a failed delete")
		}
	})

	t.Run("save failure lands on the console status", func(t *testing.T) {
		c := newTestConsole(t)
		addTestKey(t, c, "id")
		addTestServer(t, c, "web")
		lockStoreDir(t, c)
		v := c.deleteServerView(c.store.Servers[0])
		v.Handle(rn('y'))
		if c.status == "" {
			t.Error("save failure not shown on the console status")
		}
	})
}

// lockStoreDir makes the store dir non-writable so Save fails.
func lockStoreDir(t *testing.T, c *Console) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("chmod cannot block writes for root")
	}
	dir := c.store.Dir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}
