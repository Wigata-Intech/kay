// White-box (package app): drives the masked-input modal view directly.
package app

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestSecretView(t *testing.T) {
	t.Run("enter submits the typed value", func(t *testing.T) {
		var gotV string
		var gotOK bool
		v := &secretView{title: "T", label: "Passphrase", respond: func(val string, ok bool) { gotV, gotOK = val, ok }}
		for _, r := range "hunter2" {
			v.Handle(rn(r))
		}
		if act := v.Handle(key(tui.KeyEnter)); act.kind != actPop {
			t.Errorf("action = %v, want pop", act.kind)
		}
		if gotV != "hunter2" || !gotOK {
			t.Errorf("respond got (%q, %v), want (hunter2, true)", gotV, gotOK)
		}
	})

	t.Run("esc cancels", func(t *testing.T) {
		var gotOK bool
		v := &secretView{respond: func(_ string, ok bool) { gotOK = ok }}
		v.Handle(rn('x'))
		if act := v.Handle(key(tui.KeyEsc)); act.kind != actPop {
			t.Errorf("action = %v, want pop", act.kind)
		}
		if gotOK {
			t.Error("respond got ok=true, want canceled")
		}
	})

	t.Run("unhandled keys wait", func(t *testing.T) {
		v := &secretView{respond: func(string, bool) { t.Error("respond called") }}
		if act := v.Handle(key(tui.KeyTab)); act.kind != actNone {
			t.Errorf("action = %v, want none", act.kind)
		}
	})

	t.Run("draw masks the value", func(t *testing.T) {
		v := &secretView{title: "T", label: "Passphrase", input: tui.TextInput{Masked: true}, respond: func(string, bool) {}}
		for _, r := range "secret" {
			v.Handle(rn(r))
		}
		joined := strings.Join(v.Draw(60, 20), "\n")
		if strings.Contains(joined, "secret") || !strings.Contains(joined, "******") {
			t.Errorf("Draw leaked the value: %q", joined)
		}
		if !strings.Contains(joined, "Passphrase") {
			t.Errorf("Draw missing the label: %q", joined)
		}
	})

	t.Run("narrow screens keep a minimum input window", func(t *testing.T) {
		v := &secretView{label: strings.Repeat("l", 30), respond: func(string, bool) {}}
		if got := v.Draw(20, 10); len(got) != 10 {
			t.Errorf("Draw lines = %d, want 10", len(got))
		}
	})

	t.Run("title is the lowercased subject", func(t *testing.T) {
		if got := (&secretView{title: "Encrypted key"}).Title(); got != "encrypted key" {
			t.Errorf("Title() = %q", got)
		}
	})
}
