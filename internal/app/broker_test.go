// White-box (package app): drives the prompt broker through the stack driver.
package app

import (
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// waitDrawn polls until a frame containing s has been drawn; on timeout it
// feeds nothing and leaves the failure to the caller's own deadline.
func waitDrawn(scr *fakeScreen, s string) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !scr.contains(s) {
		time.Sleep(time.Millisecond)
	}
}

func TestBrokerThroughDrive(t *testing.T) {
	t.Run("AskYesNo surfaces as a modal and returns the answer", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "base", lines: []string{"base body"}, handle: popOnEsc})

		got := make(chan bool, 1)
		go func() { got <- c.AskYesNo("Unknown host", []string{"trust?"}) }()

		ch := make(chan tui.Event, 4)
		scr := newScreen()
		go func() {
			// Answer the modal only once it is actually on screen, then close
			// the base view.
			waitDrawn(scr, "Unknown host")
			ch <- rn('y')
			ch <- key(tui.KeyEsc)
		}()
		if quit := c.drive(scr, ch); quit {
			t.Error("drive() = quit, want return to base")
		}
		select {
		case ok := <-got:
			if !ok {
				t.Error("AskYesNo = false, want the y answer")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("AskYesNo never returned")
		}
		if !scr.contains("Unknown host") {
			t.Error("prompt modal never drawn")
		}
	})

	t.Run("AskSecret returns the typed value", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "base", handle: popOnEsc})

		type answer struct {
			v  string
			ok bool
		}
		got := make(chan answer, 1)
		go func() {
			v, ok := c.AskSecret("Encrypted key", "Passphrase")
			got <- answer{v, ok}
		}()

		ch := make(chan tui.Event, 8)
		scr := newScreen()
		go func() {
			waitDrawn(scr, "Passphrase")
			for _, r := range "pw" {
				ch <- rn(r)
			}
			ch <- key(tui.KeyEnter)
			ch <- key(tui.KeyEsc)
		}()
		if quit := c.drive(scr, ch); quit {
			t.Error("drive() = quit, want return to base")
		}
		select {
		case a := <-got:
			if a.v != "pw" || !a.ok {
				t.Errorf("AskSecret = (%q, %v), want (pw, true)", a.v, a.ok)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("AskSecret never returned")
		}
		if scr.contains("pw") {
			t.Error("typed secret rendered in a frame")
		}
	})

	t.Run("console exit fails pending prompts closed", func(t *testing.T) {
		c := newTestConsole(t)
		yes := make(chan bool, 1)
		secret := make(chan bool, 1)
		go func() { yes <- c.AskYesNo("T", nil) }()
		go func() { _, ok := c.AskSecret("T", "l"); secret <- ok }()

		// Run with no servers and an immediate Ctrl-C (intercepted whatever is
		// on top): pending prompts must not block the askers forever.
		if err := c.Run(newScreen(), send(tui.Event{Type: tui.EventQuit})); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		for name, ch := range map[string]chan bool{"AskYesNo": yes, "AskSecret": secret} {
			select {
			case ok := <-ch:
				if ok {
					t.Errorf("%s = true after exit, want fail-closed", name)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s never released after Run exit", name)
			}
		}
	})

	t.Run("multiple queued prompts all surface", func(t *testing.T) {
		c := newTestConsole(t)
		c.Push(&fakeView{title: "base", handle: popOnEsc})
		first := make(chan bool, 1)
		second := make(chan bool, 1)
		go func() { first <- c.AskYesNo("first prompt", nil) }()
		go func() { second <- c.AskYesNo("second prompt", nil) }()

		ch := make(chan tui.Event, 4)
		scr := newScreen()
		go func() {
			// Answer the top prompt when either is visible; the other draws
			// after the pop, then the base view closes.
			waitDrawn(scr, "prompt")
			ch <- rn('y')
			waitDrawn(scr, "first prompt")
			waitDrawn(scr, "second prompt")
			ch <- rn('n')
			ch <- key(tui.KeyEsc)
		}()
		c.drive(scr, ch)
		answers := 0
		for _, respCh := range []chan bool{first, second} {
			select {
			case <-respCh:
				answers++
			case <-time.After(2 * time.Second):
			}
		}
		if answers != 2 {
			t.Errorf("prompts answered = %d, want 2", answers)
		}
		if !scr.contains("first prompt") || !scr.contains("second prompt") {
			t.Error("not every queued prompt was drawn")
		}
	})
}

func TestPostDoorbell(t *testing.T) {
	c := newTestConsole(t)
	c.pushLater(&fakeView{title: "p1"})
	c.pushLater(&fakeView{title: "p2"}) // doorbell already rung: must not block
	c.runPosted()
	if len(c.stack) != 2 {
		t.Errorf("stack size = %d, want both queued prompts", len(c.stack))
	}
	c.runPosted() // drained queue: no change
	if len(c.stack) != 2 {
		t.Errorf("stack size after empty drain = %d, want 2", len(c.stack))
	}
}
