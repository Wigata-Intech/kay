// White-box: tests for overview.go — the customisable Overview layout resolver,
// its editor, and persistence, reaching unexported model state.
package dashboard

import (
	"errors"
	"strings"
	"testing"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

func names(panels []config.PanelPref) []string {
	out := make([]string, len(panels))
	for i, p := range panels {
		out[i] = p.Name
	}
	return out
}

func TestEffectiveLayout(t *testing.T) {
	tests := []struct {
		name  string
		saved []config.PanelPref
		want  []string
	}{
		{name: "nil is the default order", saved: nil, want: []string{"system", "procs", "net", "docker"}},
		{
			name:  "custom order, missing panels appended",
			saved: []config.PanelPref{{Name: "docker"}, {Name: "system", Hidden: true}},
			want:  []string{"docker", "system", "procs", "net"},
		},
		{
			name:  "unknown names dropped",
			saved: []config.PanelPref{{Name: "bogus"}, {Name: "net"}},
			want:  []string{"net", "system", "procs", "docker"},
		},
		{
			name:  "duplicates collapsed",
			saved: []config.PanelPref{{Name: "system"}, {Name: "system"}},
			want:  []string{"system", "procs", "net", "docker"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &model{overviewLayout: tt.saved}
			if got := names(m.effectiveLayout()); strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("effectiveLayout = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEffectiveLayoutPreservesHidden(t *testing.T) {
	m := &model{overviewLayout: []config.PanelPref{{Name: "system", Hidden: true}}}
	got := m.effectiveLayout()
	if !got[0].Hidden {
		t.Errorf("system should stay hidden, got %+v", got[0])
	}
	for _, p := range got[1:] { // appended panels default to visible
		if p.Hidden {
			t.Errorf("appended panel %q should be visible", p.Name)
		}
	}
}

func TestRenderOverviewCustom(t *testing.T) {
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = true })

	t.Run("order respected, hidden skipped", func(t *testing.T) {
		m := &model{overviewLayout: []config.PanelPref{
			{Name: "docker"}, {Name: "system", Hidden: true}, {Name: "net", Hidden: true}, {Name: "procs", Hidden: true},
		}}
		out := strings.Join(m.renderOverview(120), "\n")
		if !strings.Contains(out, "Docker") {
			t.Errorf("visible Docker panel missing: %q", out)
		}
		if strings.Contains(out, "System") || strings.Contains(out, "Top processes") {
			t.Errorf("hidden panels leaked: %q", out)
		}
	})

	t.Run("all hidden shows a hint", func(t *testing.T) {
		m := &model{overviewLayout: []config.PanelPref{
			{Name: "system", Hidden: true}, {Name: "procs", Hidden: true},
			{Name: "net", Hidden: true}, {Name: "docker", Hidden: true},
		}}
		out := strings.Join(m.renderOverview(120), "\n")
		if !strings.Contains(out, "all panels hidden") {
			t.Errorf("expected all-hidden hint, got %q", out)
		}
	})
}

func TestRenderOverviewCustomAllPanels(t *testing.T) {
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = true })

	m := &model{
		overviewLayout: []config.PanelPref{{Name: "system"}, {Name: "procs"}, {Name: "net"}, {Name: "docker"}},
		snap: metrics.Snapshot{
			CPUPercent: 12, MemUsedPercent: 40, NumCPU: 4, Load1: 0.5,
			Procs:  []metrics.Proc{{PID: 1, Name: "init", CPU: 1, Mem: 2}},
			Net:    []metrics.NetIface{{Name: "eth0", RxBytes: 100, TxBytes: 50}},
			Docker: []metrics.Container{{Name: "web", Status: "Up (healthy)"}}, DockerPresent: true,
		},
	}
	out := strings.Join(m.renderOverview(120), "\n")
	for _, want := range []string{"System", "Top processes", "Network", "Docker"} {
		if !strings.Contains(out, want) {
			t.Errorf("custom overview missing %q panel:\n%s", want, out)
		}
	}
}

func TestRenderLayoutEditor(t *testing.T) {
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = true })

	m := &model{}
	m.openLayoutEditor()
	m.layoutEdit.panels[1].Hidden = true // hide "procs"
	out := m.renderLayoutEditor()
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "System") || !strings.Contains(joined, "Top processes") {
		t.Errorf("editor should list panel titles: %q", joined)
	}
	if !strings.Contains(joined, "[x]") || !strings.Contains(joined, "[ ]") {
		t.Errorf("editor should show visible [x] and hidden [ ] marks: %q", joined)
	}
}

func TestLayoutEditorReorderAndHide(t *testing.T) {
	m := &model{}
	m.openLayoutEditor()
	if m.layoutEdit == nil || len(m.layoutEdit.panels) != 4 {
		t.Fatalf("editor should start with 4 panels")
	}

	// j moves down, J swaps the second panel up to the top.
	m.handleLayoutEditKey(tui.Event{Rune: 'j'})
	m.handleLayoutEditKey(tui.Event{Rune: 'K'})
	if got := m.layoutEdit.panels[0].Name; got != "procs" {
		t.Errorf("after moving up, top panel = %q, want procs", got)
	}
	if m.layoutEdit.sel != 0 {
		t.Errorf("selection should follow the moved panel, sel = %d", m.layoutEdit.sel)
	}

	// space toggles visibility of the selected panel.
	m.handleLayoutEditKey(tui.Event{Rune: ' '})
	if !m.layoutEdit.panels[0].Hidden {
		t.Error("space should hide the selected panel")
	}
}

func TestLayoutEditorCancelDiscards(t *testing.T) {
	m := &model{}
	m.openLayoutEditor()
	m.handleLayoutEditKey(tui.Event{Rune: 'J'}) // mutate the editor copy
	m.handleLayoutEditKey(tui.Event{Key: tui.KeyEsc})
	if m.layoutEdit != nil {
		t.Error("Esc should close the editor")
	}
	if m.overviewLayout != nil {
		t.Error("Esc should discard changes (layout stays default)")
	}
}

func TestApplyLayoutPersists(t *testing.T) {
	tui.ColorEnabled = false
	t.Cleanup(func() { tui.ColorEnabled = true })

	t.Run("saves via the injected saver", func(t *testing.T) {
		var saved []config.PanelPref
		m := &model{saveLayout: func(p []config.PanelPref) error { saved = p; return nil }}
		m.openLayoutEditor()
		m.handleLayoutEditKey(tui.Event{Rune: 'w'})
		if m.layoutEdit != nil {
			t.Error("save should close the editor")
		}
		if len(saved) != 4 {
			t.Fatalf("saver got %d panels, want 4", len(saved))
		}
		if m.overviewLayout == nil {
			t.Error("layout should be applied to the model")
		}
		if !strings.Contains(m.status, "saved") {
			t.Errorf("status = %q, want a saved confirmation", m.status)
		}
	})

	t.Run("surfaces a save error", func(t *testing.T) {
		m := &model{saveLayout: func([]config.PanelPref) error { return errors.New("disk full") }}
		m.openLayoutEditor()
		m.handleLayoutEditKey(tui.Event{Rune: 'w'})
		if !strings.Contains(m.status, "disk full") {
			t.Errorf("status = %q, want the save error", m.status)
		}
	})

	t.Run("applies without a saver", func(t *testing.T) {
		m := &model{}
		m.openLayoutEditor()
		m.applyLayout(m.layoutEdit.panels)
		if m.overviewLayout == nil {
			t.Error("layout should apply even with no saver")
		}
	})
}

func TestOverviewActionOpensEditor(t *testing.T) {
	m := &model{tab: tabOverview}
	m.overviewAction(tui.Event{Rune: 'o'})
	if m.layoutEdit == nil {
		t.Error("'o' should open the layout editor")
	}
	m2 := &model{tab: tabOverview}
	m2.overviewAction(tui.Event{Rune: 'x'})
	if m2.layoutEdit != nil {
		t.Error("other keys should not open the editor")
	}
}
