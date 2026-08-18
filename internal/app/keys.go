package app

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/keys"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// keysView is the `K` screen: the stored keys with generate/show/delete.
type keysView struct {
	c    *Console
	list tui.List
}

func (c *Console) keysView() View { return &keysView{c: c} }

func (*keysView) Title() string { return "keys" }

func (*keysView) Hints() string { return "n generate · s show · d delete · Esc back" }

// A SHA256 fingerprint is always 50 columns ("SHA256:" + 43 base64 runes,
// digest length fixed regardless of key type); the column is sized for it.
const fpColWidth = 50

func (v *keysView) Draw(w, h int) []string {
	cw := w
	if cw > 120 {
		cw = 120
	}
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}
	if len(v.c.store.Keys) == 0 {
		body := []string{
			"",
			"  No keys yet.",
			"",
			"  Press " + tui.Bold("n") + " to generate one.",
			"",
		}
		return tui.ClampAll(tui.Box("Keys", body, cw, len(body)), w, h)
	}
	v.list.Header = fmt.Sprintf("%s %-8s %s %s",
		tui.Pad("NAME", 14), "TYPE", tui.Pad("FINGERPRINT", fpColWidth), "CREATED")
	rows := make([]string, len(v.c.store.Keys))
	for i, k := range v.c.store.Keys {
		name, fp := k.Name, k.Fingerprint
		if v.c.anon() {
			name, fp = fmt.Sprintf("key-%d", i+1), "SHA256:…"
		}
		rows[i] = fmt.Sprintf("%s %-8s %s %s",
			tui.Pad(name, 14), string(k.Type), tui.Pad(fp, fpColWidth),
			k.CreatedAt.Format("2006-01-02"))
	}
	v.list.SetRows(rows)
	out := tui.Box(fmt.Sprintf("Keys — %d", len(rows)), v.list.Render(cw-4, innerH), cw, innerH)
	return tui.ClampAll(out, w, h)
}

func (v *keysView) Handle(ev tui.Event) Action {
	switch {
	case ev.Rune == 'j', ev.Key == tui.KeyDown:
		v.list.Move(1)
	case ev.Rune == 'k', ev.Key == tui.KeyUp:
		v.list.Move(-1)
	case ev.Rune == 'g', ev.Key == tui.KeyHome:
		v.list.Top()
	case ev.Rune == 'G', ev.Key == tui.KeyEnd:
		v.list.Bottom()
	case ev.Rune == 'n':
		return Push(v.c.keyGenView())
	case ev.Rune == 's':
		return v.showKey()
	case ev.Rune == 'd':
		return v.deleteKey()
	case ev.Rune == 'q', ev.Key == tui.KeyEsc:
		return Pop()
	}
	return None()
}

// selected returns the highlighted key, or nil when there are none.
func (v *keysView) selected() *config.Key {
	if i := v.list.Selected; i >= 0 && i < len(v.c.store.Keys) {
		return &v.c.store.Keys[i]
	}
	return nil
}

// showKey opens the highlighted key's authorized_keys line in a pager.
func (v *keysView) showKey() Action {
	k := v.selected()
	if k == nil {
		return None()
	}
	pub, err := keys.ReadPublic(k.PublicPath)
	if err != nil {
		v.c.status = tui.Red(err.Error())
		return None()
	}
	return Push(newPagerView("Public key "+k.Name, strings.Split(strings.TrimSpace(pub), "\n")))
}

// deleteKey confirms, then removes the highlighted key from the store and the
// key files from disk. The store refuses while a server references the key.
func (v *keysView) deleteKey() Action {
	k := v.selected()
	if k == nil {
		return None()
	}
	name, priv, pub := k.Name, k.PrivatePath, k.PublicPath
	return Push(&confirmView{
		title: "Delete key",
		text:  []string{fmt.Sprintf("Remove key %s and its files?", name)},
		respond: func(ok bool) {
			if !ok {
				return
			}
			if err := v.c.removeKey(name, priv, pub); err != nil {
				v.c.status = tui.Red(err.Error())
			}
		},
	})
}

// removeKey drops the key from the store, persists, then deletes the files.
func (c *Console) removeKey(name, privPath, pubPath string) error {
	if err := c.store.RemoveKey(name); err != nil {
		return err
	}
	if err := c.store.Save(); err != nil {
		return err
	}
	return errors.Join(os.Remove(privPath), os.Remove(pubPath))
}

// Key-gen form field order.
const (
	fldKeyName = iota
	fldKeyType
	fldKeyBits
	numKeyFields
)

// keyGenView is the `n` screen inside the keys view: generate a key pair.
func (c *Console) keyGenView() View {
	f := tui.Form{Fields: []tui.Field{{Label: "Name"}, {Label: "Type"}, {Label: "Bits (rsa)"}}}
	f.Fields[fldKeyType].Input.SetValue("ed25519")
	f.Fields[fldKeyBits].Input.SetValue("3072")
	return &formView{title: "Generate key", form: f, submit: func(vals []string) ([]string, error) {
		name, typ, bits, fieldErrs := c.parseKeyGen(vals)
		if fieldErrs != nil {
			return fieldErrs, nil
		}
		return nil, c.generateKey(name, typ, bits)
	}}
}

// parseKeyGen validates the key form values.
func (c *Console) parseKeyGen(vals []string) (name string, typ config.KeyType, bits int, fieldErrs []string) {
	errs := make([]string, numKeyFields)
	bad := false
	name = strings.TrimSpace(vals[fldKeyName])
	if name == "" {
		errs[fldKeyName] = "required"
		bad = true
	} else if _, err := c.store.FindKey(name); err == nil {
		errs[fldKeyName] = "name already in use"
		bad = true
	}
	typ = config.KeyType(strings.TrimSpace(vals[fldKeyType]))
	if typ != config.KeyEd25519 && typ != config.KeyRSA {
		errs[fldKeyType] = "ed25519 or rsa"
		bad = true
	}
	bits, err := strconv.Atoi(strings.TrimSpace(vals[fldKeyBits]))
	if err != nil || bits < 0 {
		errs[fldKeyBits] = "must be a number"
		bad = true
	}
	if bad {
		return "", "", 0, errs
	}
	return name, typ, bits, nil
}

// generateKey creates, writes, and registers a new key pair.
func (c *Console) generateKey(name string, typ config.KeyType, bits int) error {
	pair, err := keys.Generate(typ, bits, name)
	if err != nil {
		return err
	}
	privPath, pubPath, err := pair.Write(c.store.KeysDir(), name)
	if err != nil {
		return err
	}
	if err := c.store.AddKey(config.Key{
		Name: name, Type: typ,
		PrivatePath: privPath, PublicPath: pubPath,
		Fingerprint: pair.Fingerprint, CreatedAt: time.Now(),
	}); err != nil {
		return err
	}
	return c.store.Save()
}
