package app

import "github.com/Wigata-Intech/kay/internal/tui"

// The prompt broker lets any goroutine — typically a background SSH dial that
// hit an unknown host key or an encrypted key file, or an async command
// finishing — hand work to the UI goroutine. post queues a closure and rings
// the wake doorbell, which both the fleet loop (Options.Interrupt) and drive
// select on; the closures run on the UI goroutine, so they may touch views
// and the stack freely.

// AskYesNo blocks until the user answers the prompt with y/n (Esc = no), or
// the console exits (= no).
func (c *Console) AskYesNo(title string, text []string) bool {
	resp := make(chan bool, 1)
	c.pushLater(&confirmView{title: title, text: text, respond: func(ok bool) { resp <- ok }})
	select {
	case ok := <-resp:
		return ok
	case <-c.done:
		return false // console gone: fail closed
	}
}

// AskSecret blocks until the user enters a masked value (ok=false when they
// cancel with Esc or the console exits).
func (c *Console) AskSecret(title, label string) (secret string, ok bool) {
	type answer struct {
		value string
		ok    bool
	}
	resp := make(chan answer, 1)
	c.pushLater(&secretView{title: title, label: label, input: tui.TextInput{Masked: true},
		respond: func(v string, ok bool) {
			resp <- answer{value: v, ok: ok}
		}})
	select {
	case a := <-resp:
		return a.value, a.ok
	case <-c.done:
		return "", false
	}
}

// pushLater queues v for the UI goroutine to push on top of the stack.
func (c *Console) pushLater(v View) {
	c.post(func() { c.Push(v) })
}

// post queues fn for the UI goroutine and rings the doorbell.
func (c *Console) post(fn func()) {
	c.mu.Lock()
	c.queue = append(c.queue, fn)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default: // doorbell already rung
	}
}

// runPosted executes every queued closure on the UI goroutine.
func (c *Console) runPosted() {
	c.mu.Lock()
	pending := c.queue
	c.queue = nil
	c.mu.Unlock()
	for _, fn := range pending {
		fn()
	}
}
