// White-box (package app): drives the notice modal view directly.
package app

import (
	"strings"
	"testing"
)

func TestNoticeView(t *testing.T) {
	v := &noticeView{title: "Key installed", text: []string{"done"}}
	joined := strings.Join(v.Draw(60, 20), "\n")
	for _, want := range []string{"Key installed", "done", "any key to dismiss"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Draw missing %q", want)
		}
	}
	if act := v.Handle(rn('x')); act.kind != actPop {
		t.Errorf("Handle = %v, want pop on any key", act.kind)
	}
	if got := v.Title(); got != "key installed" {
		t.Errorf("Title() = %q", got)
	}
}
