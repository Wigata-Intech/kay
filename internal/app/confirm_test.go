// White-box (package app): drives the confirm modal view directly.
package app

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestConfirmView(t *testing.T) {
	tests := []struct {
		name   string
		ev     tui.Event
		want   bool
		popped bool
	}{
		{"y answers yes", rn('y'), true, true},
		{"enter answers yes", key(tui.KeyEnter), true, true},
		{"n answers no", rn('n'), false, true},
		{"esc answers no", key(tui.KeyEsc), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *bool
			v := &confirmView{title: "T", text: []string{"sure?"}, respond: func(ok bool) { got = &ok }}
			act := v.Handle(tt.ev)
			if got == nil || *got != tt.want {
				t.Fatalf("respond got %v, want %v", got, tt.want)
			}
			if (act.kind == actPop) != tt.popped {
				t.Errorf("action = %v, want pop", act.kind)
			}
		})
	}

	t.Run("other keys wait", func(t *testing.T) {
		v := &confirmView{respond: func(bool) { t.Error("respond called") }}
		if act := v.Handle(rn('x')); act.kind != actNone {
			t.Errorf("action = %v, want none", act.kind)
		}
	})

	t.Run("draw shows title, text, and hint", func(t *testing.T) {
		v := &confirmView{title: "Delete key", text: []string{"remove id?"}}
		joined := strings.Join(v.Draw(60, 20), "\n")
		for _, want := range []string{"Delete key", "remove id?", "y yes"} {
			if !strings.Contains(joined, want) {
				t.Errorf("Draw missing %q", want)
			}
		}
	})

	t.Run("title is the lowercased subject", func(t *testing.T) {
		if got := (&confirmView{title: "Delete key"}).Title(); got != "delete key" {
			t.Errorf("Title() = %q", got)
		}
	})
}
