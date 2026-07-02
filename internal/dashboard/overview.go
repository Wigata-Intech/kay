package dashboard

import (
	"fmt"

	"github.com/Wigata-Intech/kay/internal/config"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// overviewPanels is the canonical set of Overview panels, in built-in order. A
// saved layout references these by name; unknown names are ignored (forward
// compatibility) and any known panel missing from a saved layout is appended.
var overviewPanels = []struct{ name, title string }{
	{"system", "System"},
	{"procs", "Top processes"},
	{"net", "Network"},
	{"docker", "Docker"},
}

func panelTitle(name string) string {
	for _, p := range overviewPanels {
		if p.name == name {
			return p.title
		}
	}
	return ""
}

// defaultPanelPrefs is the built-in Overview layout: every panel, default order.
func defaultPanelPrefs() []config.PanelPref {
	out := make([]config.PanelPref, len(overviewPanels))
	for i, p := range overviewPanels {
		out[i] = config.PanelPref{Name: p.name}
	}
	return out
}

// effectiveLayout returns the model's saved layout (or the default when unset),
// dropping unknown/duplicate names and appending any known panel a saved layout
// omits — so adding a panel in a later version can't hide it from existing users.
func (m *model) effectiveLayout() []config.PanelPref {
	if m.overviewLayout == nil {
		return defaultPanelPrefs()
	}
	seen := make(map[string]bool, len(overviewPanels))
	out := make([]config.PanelPref, 0, len(overviewPanels))
	for _, p := range m.overviewLayout {
		if panelTitle(p.Name) == "" || seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	for _, p := range overviewPanels {
		if !seen[p.name] {
			out = append(out, config.PanelPref{Name: p.name})
		}
	}
	return out
}

// renderPanel renders one Overview panel block (a cyan title plus its body).
func (m *model) renderPanel(name string) []string {
	s := m.snap
	switch name {
	case "system":
		return append([]string{tui.Cyan("System")}, m.overviewSystem(s)...)
	case "procs":
		return append([]string{tui.Cyan("Top processes")}, m.overviewProcs(s, 6)...)
	case "net":
		net := m.overviewNet(4)
		if len(net) == 0 {
			net = []string{tui.Dim("no active interfaces")}
		}
		return append([]string{tui.Cyan("Network")}, net...)
	case "docker":
		return []string{tui.Cyan("Docker"), m.overviewDocker(s)}
	}
	return nil
}

// renderOverviewCustom renders the Overview as a vertical stack of the user's
// visible panels, in their saved order. (The default, uncustomised layout keeps
// the two-column rendering in renderOverview.)
func (m *model) renderOverviewCustom() []string {
	var L []string
	for _, p := range m.effectiveLayout() {
		if p.Hidden {
			continue
		}
		block := m.renderPanel(p.Name)
		if len(block) == 0 {
			continue
		}
		if len(L) > 0 {
			L = append(L, "")
		}
		L = append(L, block...)
	}
	if len(L) == 0 {
		return []string{"", tui.Dim("all panels hidden — press o to customise the Overview")}
	}
	return L
}

// layoutEditor is the interactive Overview panel reorder/hide overlay.
type layoutEditor struct {
	panels []config.PanelPref
	sel    int
}

// openLayoutEditor starts editing from the current effective layout (a copy, so
// Esc discards changes).
func (m *model) openLayoutEditor() {
	eff := m.effectiveLayout()
	cp := make([]config.PanelPref, len(eff))
	copy(cp, eff)
	m.layoutEdit = &layoutEditor{panels: cp}
}

// handleLayoutEditKey drives the layout editor: j/k select, J/K move, space
// toggles visibility, w saves, Esc/q cancels.
func (m *model) handleLayoutEditKey(ev tui.Event) {
	e := m.layoutEdit
	switch {
	case ev.Key == tui.KeyUp, ev.Rune == 'k':
		if e.sel > 0 {
			e.sel--
		}
	case ev.Key == tui.KeyDown, ev.Rune == 'j':
		if e.sel < len(e.panels)-1 {
			e.sel++
		}
	case ev.Rune == 'K':
		if e.sel > 0 {
			e.panels[e.sel-1], e.panels[e.sel] = e.panels[e.sel], e.panels[e.sel-1]
			e.sel--
		}
	case ev.Rune == 'J':
		if e.sel < len(e.panels)-1 {
			e.panels[e.sel+1], e.panels[e.sel] = e.panels[e.sel], e.panels[e.sel+1]
			e.sel++
		}
	case ev.Rune == ' ':
		e.panels[e.sel].Hidden = !e.panels[e.sel].Hidden
	case ev.Rune == 'w':
		m.applyLayout(e.panels)
	case ev.Key == tui.KeyEsc, ev.Rune == 'q':
		m.layoutEdit = nil
	}
}

// applyLayout activates the edited layout and persists it via the injected saver.
func (m *model) applyLayout(panels []config.PanelPref) {
	cp := make([]config.PanelPref, len(panels))
	copy(cp, panels)
	m.overviewLayout = cp
	m.layoutEdit = nil
	if m.saveLayout == nil {
		m.status = tui.Green("overview layout applied")
		return
	}
	if err := m.saveLayout(cp); err != nil {
		m.status = tui.Red("save layout: " + tui.FirstLine(err.Error()))
		return
	}
	m.status = tui.Green("overview layout saved")
}

// renderLayoutEditor renders the editor overlay body: one row per panel with a
// visibility checkbox and the moving cursor.
func (m *model) renderLayoutEditor() []string {
	e := m.layoutEdit
	out := make([]string, 0, len(e.panels)+2)
	out = append(out, tui.Dim("reorder and hide Overview panels — changes apply on save"), "")
	for i, p := range e.panels {
		cursor := "  "
		if i == e.sel {
			cursor = tui.Cyan("▌ ")
		}
		mark, title := tui.Green("[x]"), panelTitle(p.Name)
		if p.Hidden {
			mark, title = tui.Dim("[ ]"), tui.Dim(title)
		}
		out = append(out, fmt.Sprintf("%s%s %s", cursor, mark, title))
	}
	return out
}
