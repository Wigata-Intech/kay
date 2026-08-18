// White-box (package app): drives the pager view directly.
package app

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestPagerView(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "row"
	}

	t.Run("draw frames the content", func(t *testing.T) {
		v := newPagerView("Public key id", []string{"ssh-ed25519 AAAA id"})
		joined := strings.Join(v.Draw(80, 24), "\n")
		for _, want := range []string{"Public key id", "ssh-ed25519 AAAA id"} {
			if !strings.Contains(joined, want) {
				t.Errorf("Draw missing %q", want)
			}
		}
	})

	t.Run("scroll keys move the window", func(t *testing.T) {
		v := newPagerView("T", lines)
		for _, ev := range []tui.Event{rn('j'), rn('j'), rn('k'), rn('G'), rn('g'), key(tui.KeyDown), key(tui.KeyUp), key(tui.KeyEnd), key(tui.KeyHome)} {
			if act := v.Handle(ev); act.kind != actNone {
				t.Fatalf("Handle(%+v) = %v, want none", ev, act.kind)
			}
		}
		v.Handle(rn('G'))
		if start, _ := v.pager.Window(10); start == 0 {
			t.Error("G did not scroll to the bottom")
		}
	})

	t.Run("esc and q pop", func(t *testing.T) {
		for _, ev := range []tui.Event{key(tui.KeyEsc), rn('q')} {
			v := newPagerView("T", lines)
			if act := v.Handle(ev); act.kind != actPop {
				t.Errorf("Handle(%+v) = %v, want pop", ev, act.kind)
			}
		}
	})

	t.Run("tiny screens keep one content line", func(t *testing.T) {
		v := newPagerView("T", lines)
		if got := v.Draw(20, 2); len(got) != 2 {
			t.Errorf("Draw lines = %d, want 2", len(got))
		}
	})

	t.Run("wide terminals clamp the content width", func(t *testing.T) {
		v := newPagerView("T", lines)
		for _, l := range v.Draw(200, 24) {
			if w := tui.VisibleWidth(l); w > 120 {
				t.Errorf("line wider than the 120-column clamp: %d", w)
			}
		}
	})

	t.Run("title and hints name the view", func(t *testing.T) {
		v := newPagerView("Public key id", nil)
		if got := v.Title(); got != "public key id" {
			t.Errorf("Title() = %q", got)
		}
		if !strings.Contains(v.Hints(), "j/k scroll") {
			t.Errorf("Hints() = %q", v.Hints())
		}
	})
}
