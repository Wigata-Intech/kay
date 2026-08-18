package tui_test

import (
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func modalFrame() []string {
	base := make([]string, 12)
	for i := range base {
		base[i] = "background row"
	}
	return base
}

func TestModalRender(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	m := &tui.Modal{Title: "Confirm", Text: []string{"delete server web?"}, Hint: "y confirm · n cancel"}
	got := m.Render(modalFrame(), 60, 12)

	if len(got) != 12 {
		t.Fatalf("Render returned %d lines, want 12", len(got))
	}
	for i, l := range got {
		if w := tui.VisibleWidth(l); w != 60 {
			t.Errorf("line %d visible width = %d, want 60", i, w)
		}
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"Confirm", "delete server web?", "y confirm · n cancel", "┌", "└"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Render missing %q", want)
		}
	}
}

func TestModalRenderEdges(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = false
	defer func() { tui.ColorEnabled = old }()

	t.Run("wide content clamps to the frame", func(t *testing.T) {
		m := &tui.Modal{Title: "T", Text: []string{strings.Repeat("x", 100)}}
		got := m.Render(modalFrame(), 20, 12)
		for i, l := range got {
			if w := tui.VisibleWidth(l); w != 20 {
				t.Errorf("line %d visible width = %d, want 20", i, w)
			}
		}
	})

	t.Run("frame narrower than a box just dims the base", func(t *testing.T) {
		m := &tui.Modal{Title: "T", Text: []string{"body"}}
		got := m.Render([]string{"background"}, 3, 2)
		if len(got) != 2 {
			t.Fatalf("Render returned %d lines, want 2", len(got))
		}
		for i, l := range got {
			if w := tui.VisibleWidth(l); w != 3 {
				t.Errorf("line %d visible width = %d, want 3", i, w)
			}
		}
	})

	t.Run("box taller than the frame is clipped", func(t *testing.T) {
		m := &tui.Modal{Title: "T", Text: []string{"1", "2", "3", "4", "5"}}
		if got := m.Render(nil, 20, 3); len(got) != 3 {
			t.Errorf("Render returned %d lines, want 3", len(got))
		}
	})

	t.Run("no hint renders without a hint row", func(t *testing.T) {
		m := &tui.Modal{Title: "T", Text: []string{"body"}}
		if got := strings.Join(m.Render(modalFrame(), 40, 12), "\n"); !strings.Contains(got, "body") {
			t.Errorf("Render missing body: %q", got)
		}
	})
}

func TestModalDimsAndStripsBase(t *testing.T) {
	old := tui.ColorEnabled
	tui.ColorEnabled = true
	defer func() { tui.ColorEnabled = old }()

	base := []string{tui.Red("alert row"), "plain row", "third row", "fourth row"}
	m := &tui.Modal{Title: "T", Text: []string{"hi"}}
	got := m.Render(base, 30, 4)

	if strings.Contains(got[0], "\x1b[31m") {
		t.Errorf("base colour survived the overlay: %q", got[0])
	}
	if !strings.Contains(got[0], "\x1b[2m") {
		t.Errorf("base row not dimmed: %q", got[0])
	}
}
