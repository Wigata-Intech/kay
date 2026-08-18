// White-box (package app): drives the keys view and key-gen form directly.
package app

import (
	"os"
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// genKey generates a real ed25519 key through the console's own path.
func genKey(t *testing.T, c *Console, name string) {
	t.Helper()
	if err := c.generateKey(name, config.KeyEd25519, 0); err != nil {
		t.Fatalf("generate key: %v", err)
	}
}

func TestKeysViewDraw(t *testing.T) {
	c := newTestConsole(t)
	genKey(t, c, "laptop")
	v := c.keysView()
	joined := strings.Join(v.Draw(100, 24), "\n")
	for _, want := range []string{"Keys — 1", "laptop", "ed25519", "SHA256:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Draw missing %q", want)
		}
	}
	if got := v.Title(); got != "keys" {
		t.Errorf("Title() = %q", got)
	}

	t.Run("demo mode masks names and fingerprints", func(t *testing.T) {
		c := newTestConsole(t)
		c.fleetOpts.Anonymize = true
		genKey(t, c, "laptop")
		joined := strings.Join(c.keysView().Draw(100, 24), "\n")
		if strings.Contains(joined, "laptop") || strings.Contains(joined, "SHA256:T") {
			t.Errorf("demo keys view leaks identity: %q", joined)
		}
		for _, want := range []string{"key-1", "SHA256:…"} {
			if !strings.Contains(joined, want) {
				t.Errorf("demo keys view missing %q", want)
			}
		}
	})

	t.Run("empty store shows the empty state, not a bare box", func(t *testing.T) {
		c := newTestConsole(t)
		joined := strings.Join(c.keysView().Draw(100, 24), "\n")
		if !strings.Contains(joined, "No keys yet") || !strings.Contains(joined, "n") {
			t.Errorf("empty keys view = %q, want guidance", joined)
		}
	})

	t.Run("fingerprint column fits a real fingerprint", func(t *testing.T) {
		lines := v.Draw(100, 24)
		var header, row string
		for _, l := range lines {
			plain := tui.StripSGR(l)
			if strings.Contains(plain, "FINGERPRINT") {
				header = plain
			}
			if strings.Contains(plain, "laptop") {
				row = plain
			}
		}
		hi := strings.Index(header, "CREATED")
		ri := strings.Index(row, "2026-")
		if hi < 0 || ri < 0 || hi != ri {
			t.Errorf("CREATED misaligned: header col %d, row col %d", hi, ri)
		}
	})

	t.Run("wide terminals clamp, tiny terminals keep a row", func(t *testing.T) {
		for _, l := range v.Draw(200, 30) {
			if w := tui.VisibleWidth(l); w > 120 {
				t.Errorf("line wider than the 120-column clamp: %d", w)
			}
		}
		if got := v.Draw(100, 2); len(got) != 2 {
			t.Errorf("tiny Draw lines = %d, want 2", len(got))
		}
	})
}

func TestKeysViewHandle(t *testing.T) {
	t.Run("navigation keys stay in the view", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "a")
		genKey(t, c, "b")
		v := c.keysView().(*keysView)
		v.Draw(100, 24) // populate the list rows
		for _, ev := range []tui.Event{rn('j'), rn('k'), rn('G'), rn('g'), key(tui.KeyDown), key(tui.KeyUp), key(tui.KeyEnd), key(tui.KeyHome)} {
			if act := v.Handle(ev); act.kind != actNone {
				t.Fatalf("Handle(%+v) = %v, want none", ev, act.kind)
			}
		}
	})

	t.Run("n pushes the key-gen form", func(t *testing.T) {
		c := newTestConsole(t)
		v := c.keysView()
		act := v.Handle(rn('n'))
		if act.kind != actPush {
			t.Fatalf("Handle(n) = %v, want push", act.kind)
		}
		if _, ok := act.next.(*formView); !ok {
			t.Errorf("pushed view = %T, want the key-gen form", act.next)
		}
	})

	t.Run("s shows the public key in a pager", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "laptop")
		v := c.keysView().(*keysView)
		v.Draw(100, 24)
		act := v.Handle(rn('s'))
		if act.kind != actPush {
			t.Fatalf("Handle(s) = %v, want push", act.kind)
		}
		pv := act.next.(*pagerView)
		if joined := strings.Join(pv.Draw(100, 24), "\n"); !strings.Contains(joined, "ssh-ed25519") {
			t.Errorf("pager missing the public key: %q", joined)
		}
	})

	t.Run("s with an unreadable public key sets the status", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "laptop")
		k, _ := c.store.FindKey("laptop")
		if err := os.Remove(k.PublicPath); err != nil {
			t.Fatalf("remove pub: %v", err)
		}
		v := c.keysView().(*keysView)
		v.Draw(100, 24)
		if act := v.Handle(rn('s')); act.kind != actNone {
			t.Fatalf("Handle(s) = %v, want none", act.kind)
		}
		if c.status == "" {
			t.Error("read failure not shown on the console status")
		}
	})

	t.Run("s and d with no keys do nothing", func(t *testing.T) {
		c := newTestConsole(t)
		v := c.keysView().(*keysView)
		v.Draw(100, 24)
		for _, r := range "sd" {
			if act := v.Handle(rn(r)); act.kind != actNone {
				t.Errorf("Handle(%c) = %v, want none", r, act.kind)
			}
		}
	})

	t.Run("esc and q pop", func(t *testing.T) {
		c := newTestConsole(t)
		for _, ev := range []tui.Event{key(tui.KeyEsc), rn('q')} {
			if act := c.keysView().Handle(ev); act.kind != actPop {
				t.Errorf("Handle(%+v) = %v, want pop", ev, act.kind)
			}
		}
	})
}

func TestKeysViewDelete(t *testing.T) {
	t.Run("confirm deletes the key and its files", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "laptop")
		k, _ := c.store.FindKey("laptop")
		priv, pub := k.PrivatePath, k.PublicPath
		v := c.keysView().(*keysView)
		v.Draw(100, 24)
		act := v.Handle(rn('d'))
		if act.kind != actPush {
			t.Fatalf("Handle(d) = %v, want push", act.kind)
		}
		act.next.Handle(rn('y'))
		if len(c.store.Keys) != 0 {
			t.Errorf("keys left = %d, want 0", len(c.store.Keys))
		}
		for _, p := range []string{priv, pub} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("key file %s still exists", p)
			}
		}
	})

	t.Run("decline keeps the key", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "laptop")
		v := c.keysView().(*keysView)
		v.Draw(100, 24)
		v.Handle(rn('d')).next.Handle(rn('n'))
		if len(c.store.Keys) != 1 {
			t.Errorf("keys left = %d, want 1", len(c.store.Keys))
		}
	})

	t.Run("a referenced key is refused with a status", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "id")
		addTestServer(t, c, "web")
		v := c.keysView().(*keysView)
		v.Draw(100, 24)
		v.Handle(rn('d')).next.Handle(rn('y'))
		if len(c.store.Keys) != 1 {
			t.Errorf("keys left = %d, want 1 (refused)", len(c.store.Keys))
		}
		if !strings.Contains(c.status, "used by server") {
			t.Errorf("status = %q, want the referenced-key refusal", c.status)
		}
	})

	t.Run("save failure keeps the entry and surfaces", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "laptop")
		lockStoreDir(t, c)
		v := c.keysView().(*keysView)
		v.Draw(100, 24)
		v.Handle(rn('d')).next.Handle(rn('y'))
		if c.status == "" {
			t.Error("save failure not shown")
		}
	})

	t.Run("missing files surface on the status", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "laptop")
		k, _ := c.store.FindKey("laptop")
		if err := os.Remove(k.PrivatePath); err != nil {
			t.Fatalf("remove priv: %v", err)
		}
		v := c.keysView().(*keysView)
		v.Draw(100, 24)
		v.Handle(rn('d')).next.Handle(rn('y'))
		if len(c.store.Keys) != 0 {
			t.Errorf("keys left = %d, want 0 (store entry removed)", len(c.store.Keys))
		}
		if c.status == "" {
			t.Error("file-removal failure not shown")
		}
	})
}

func TestParseKeyGen(t *testing.T) {
	c := newTestConsole(t)
	genKey(t, c, "taken")

	tests := []struct {
		name    string
		vals    []string
		wantErr map[int]string
	}{
		{"valid ed25519", []string{"laptop", "ed25519", "3072"}, nil},
		{"valid rsa", []string{"laptop", "rsa", "3072"}, nil},
		{"missing name", []string{"", "ed25519", "3072"}, map[int]string{fldKeyName: "required"}},
		{"duplicate name", []string{"taken", "ed25519", "3072"}, map[int]string{fldKeyName: "already in use"}},
		{"bad type", []string{"laptop", "dsa", "3072"}, map[int]string{fldKeyType: "ed25519 or rsa"}},
		{"bad bits", []string{"laptop", "rsa", "many"}, map[int]string{fldKeyBits: "number"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, typ, _, errs := c.parseKeyGen(tt.vals)
			if len(tt.wantErr) == 0 {
				if errs != nil || name != "laptop" || typ == "" {
					t.Fatalf("parseKeyGen() = %q, %q, errs %q; want valid", name, typ, errs)
				}
				return
			}
			if errs == nil {
				t.Fatal("parseKeyGen() errs = nil, want field errors")
			}
			for idx, frag := range tt.wantErr {
				if !strings.Contains(errs[idx], frag) {
					t.Errorf("field %d error = %q, want %q", idx, errs[idx], frag)
				}
			}
		})
	}
}

func TestKeyGenView(t *testing.T) {
	t.Run("valid submit generates and stores", func(t *testing.T) {
		c := newTestConsole(t)
		v := c.keyGenView()
		setField(v, fldKeyName, "laptop")
		if act := submitForm(v); act.kind != actPop {
			t.Fatalf("submit action = %v, want pop", act.kind)
		}
		k, err := c.store.FindKey("laptop")
		if err != nil {
			t.Fatalf("key not stored: %v", err)
		}
		if _, err := os.Stat(k.PrivatePath); err != nil {
			t.Errorf("private key file missing: %v", err)
		}
		if c.takeHostsDirty() {
			t.Error("key generation must not dirty the host set")
		}
	})

	t.Run("weak rsa bits surface the generator error", func(t *testing.T) {
		c := newTestConsole(t)
		v := c.keyGenView()
		setField(v, fldKeyName, "laptop")
		setField(v, fldKeyType, "rsa")
		setField(v, fldKeyBits, "512")
		if act := submitForm(v); act.kind != actNone {
			t.Fatalf("submit action = %v, want none", act.kind)
		}
		if v.(*formView).status == "" {
			t.Error("generator error not shown")
		}
	})

	t.Run("field errors keep the form open", func(t *testing.T) {
		c := newTestConsole(t)
		v := c.keyGenView()
		setField(v, fldKeyName, "")
		if act := submitForm(v); act.kind != actNone {
			t.Fatalf("submit action = %v, want none", act.kind)
		}
	})
}

func TestGenerateKeyErrors(t *testing.T) {
	t.Run("existing key file is refused", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "laptop")
		// Same file on disk, no store entry: Write refuses the overwrite.
		if err := c.store.RemoveKey("laptop"); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if err := c.generateKey("laptop", config.KeyEd25519, 0); err == nil {
			t.Error("generateKey() error = nil, want existing-file refusal")
		}
	})

	t.Run("duplicate store entry is refused", func(t *testing.T) {
		c := newTestConsole(t)
		genKey(t, c, "laptop")
		k, _ := c.store.FindKey("laptop")
		// Entry stays, files go: Write succeeds, AddKey refuses.
		for _, p := range []string{k.PrivatePath, k.PublicPath} {
			if err := os.Remove(p); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}
		if err := c.generateKey("laptop", config.KeyEd25519, 0); err == nil {
			t.Error("generateKey() error = nil, want duplicate-entry refusal")
		}
	})

	t.Run("unsupported type errors", func(t *testing.T) {
		c := newTestConsole(t)
		if err := c.generateKey("laptop", config.KeyType("dsa"), 0); err == nil {
			t.Error("generateKey() error = nil, want unsupported type")
		}
	})

	t.Run("save failure is returned", func(t *testing.T) {
		c := newTestConsole(t)
		// Keys write into KeysDir (created on demand); lock only the store
		// root afterwards so config.json cannot be written.
		if err := os.MkdirAll(c.store.KeysDir(), 0o700); err != nil {
			t.Fatalf("mkdir keys: %v", err)
		}
		lockStoreDir(t, c)
		if err := c.generateKey("laptop", config.KeyEd25519, 0); err == nil {
			t.Error("generateKey() error = nil, want save failure")
		}
	})
}
