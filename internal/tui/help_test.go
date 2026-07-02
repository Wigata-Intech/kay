package tui_test

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func TestRenderHelp(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	out := tui.RenderHelp([]tui.HelpSection{
		{Title: "Global", Keys: [][2]string{{"q", "quit"}, {"?", "help"}}},
		{Title: "Nav", Keys: [][2]string{{"j/k", "move"}}},
	})
	joined := strings.Join(out, "\n")
	for _, want := range []string{"Global", "q", "quit", "?", "help", "Nav", "j/k", "move"} {
		if !strings.Contains(joined, want) {
			t.Errorf("help missing %q:\n%s", want, joined)
		}
	}
	// A blank line separates the two sections.
	if !strings.Contains(joined, "\n\n") {
		t.Errorf("sections should be blank-separated:\n%s", joined)
	}
	// Keys are aligned: "q" is padded to the width of "j/k" (3) before its desc.
	if !strings.Contains(joined, "q    quit") { // 2 lead spaces + "q" padded to 3 + 2 spaces
		t.Errorf("keys not aligned to common width:\n%q", joined)
	}
}
