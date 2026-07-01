package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/Wigata-Intech/kay/internal/tui"
)

func (m *model) handleKey(ev tui.Event) keyResult {
	m.status = ""

	if m.detail != nil {
		return m.handleDetailKey(ev)
	}

	if m.confirm != nil {
		if ev.Rune == 'y' || ev.Rune == 'Y' {
			m.status = m.confirm.run()
		}
		m.confirm = nil
		return keyResult{refreshNow: true}
	}

	if r, handled := m.handleGlobalKey(ev); handled {
		return r
	}

	if l := m.activeList(); l != nil {
		handleListNav(l, ev)
	}

	switch m.tab {
	case tabProcesses:
		m.procAction(ev)
	case tabDocker:
		m.dockAction(ev)
	}
	return keyResult{}
}

// handleGlobalKey processes keys valid on every tab (quit, tab switching,
// refresh, interval). handled is false when ev is not one of them.
func (m *model) handleGlobalKey(ev tui.Event) (r keyResult, handled bool) {
	switch {
	case ev.Rune == 'q':
		return keyResult{quit: true}, true
	case ev.Key == tui.KeyTab:
		m.tab = (m.tab + 1) % len(tabNames)
	case ev.Key == tui.KeyShiftTab, ev.Rune == '[':
		m.tab = (m.tab + len(tabNames) - 1) % len(tabNames)
	case ev.Rune == ']':
		m.tab = (m.tab + 1) % len(tabNames)
	case ev.Rune >= '1' && ev.Rune <= '5':
		m.tab = int(ev.Rune - '1')
	case ev.Rune == 'r':
		return keyResult{refreshNow: true}, true
	case ev.Rune == '+':
		if m.interval < 60*time.Second {
			m.interval += time.Second
		}
		return keyResult{intervalChanged: true}, true
	case ev.Rune == '-':
		if m.interval > time.Second {
			m.interval -= time.Second
		}
		return keyResult{intervalChanged: true}, true
	default:
		return keyResult{}, false
	}
	return keyResult{}, true
}

// handleListNav applies cursor-movement keys to the active list.
func handleListNav(l *tui.List, ev tui.Event) {
	switch {
	case ev.Key == tui.KeyUp, ev.Rune == 'k':
		l.Move(-1)
	case ev.Key == tui.KeyDown, ev.Rune == 'j':
		l.Move(1)
	case ev.Key == tui.KeyPgUp, ev.Key == tui.KeyCtrlU:
		l.Move(-10)
	case ev.Key == tui.KeyPgDn, ev.Key == tui.KeyCtrlD:
		l.Move(10)
	case ev.Key == tui.KeyHome, ev.Rune == 'g':
		l.Top()
	case ev.Key == tui.KeyEnd, ev.Rune == 'G':
		l.Bottom()
	}
}

func (m *model) activeList() *tui.List {
	switch m.tab {
	case tabProcesses:
		return &m.proc
	case tabDocker:
		return &m.dock
	case tabNetwork:
		return &m.net
	case tabDisk:
		return &m.disk
	}
	return nil
}

func (m *model) procAction(ev tui.Event) {
	if ev.Rune == 's' { // cycle sort key, keeping the selection on the same PID
		var pid int
		if m.proc.Selected >= 0 && m.proc.Selected < len(m.snap.Procs) {
			pid = m.snap.Procs[m.proc.Selected].PID
		}
		m.sortMode = (m.sortMode + 1) % 4
		m.rebuildLists()
		for i, p := range m.snap.Procs {
			if p.PID == pid {
				m.proc.Selected = i
				break
			}
		}
		m.status = tui.Dim("sorted by " + sortName(m.sortMode))
		return
	}
	if m.proc.Selected < 0 || m.proc.Selected >= len(m.snap.Procs) {
		return
	}
	p := m.snap.Procs[m.proc.Selected]
	switch {
	case ev.Key == tui.KeyEnter:
		out, _ := m.client.Run(fmt.Sprintf(
			"cat /proc/%d/status 2>/dev/null; echo; echo 'CMDLINE:'; tr '\\0' ' ' < /proc/%d/cmdline 2>/dev/null",
			p.PID, p.PID))
		m.openDetail(fmt.Sprintf("process %d (%s)", p.PID, p.Name), out)
	case ev.Rune == 'x':
		if m.blockedReadOnly() {
			return
		}
		pid, name := p.PID, p.Name
		m.confirm = &confirmPrompt{
			text: fmt.Sprintf("terminate %s (pid %d)?", name, pid),
			run:  func() string { return m.runAction(fmt.Sprintf("kill %d", pid), "sent SIGTERM to "+name) },
		}
	case ev.Rune == 'X':
		if m.blockedReadOnly() {
			return
		}
		pid, name := p.PID, p.Name
		m.confirm = &confirmPrompt{
			text: fmt.Sprintf("FORCE kill %s (pid %d)?", name, pid),
			run:  func() string { return m.runAction(fmt.Sprintf("kill -9 %d", pid), "sent SIGKILL to "+name) },
		}
	}
}

func (m *model) dockAction(ev tui.Event) {
	if m.dock.Selected < 0 || m.dock.Selected >= len(m.snap.Docker) {
		return
	}
	c := m.snap.Docker[m.dock.Selected]
	if !validID(c.ID) {
		return
	}
	switch {
	case ev.Key == tui.KeyEnter:
		out, _ := m.client.Run("docker inspect " + c.ID + " 2>&1")
		m.openDetail("inspect "+c.Name, out)
	case ev.Rune == 'l':
		out, _ := m.client.Run("docker logs --tail 200 " + c.ID + " 2>&1")
		m.openDetail("logs "+c.Name, out)
	case ev.Rune == 'R':
		if m.blockedReadOnly() {
			return
		}
		id, name := c.ID, c.Name
		m.confirm = &confirmPrompt{
			text: fmt.Sprintf("restart container %s?", name),
			run:  func() string { return m.runAction("docker restart "+id, "restarted "+name) },
		}
	case ev.Rune == 'x':
		if m.blockedReadOnly() {
			return
		}
		id, name := c.ID, c.Name
		m.confirm = &confirmPrompt{
			text: fmt.Sprintf("stop container %s?", name),
			run:  func() string { return m.runAction("docker stop "+id, "stopped "+name) },
		}
	}
}

func (m *model) runAction(cmd, okMsg string) string {
	out, err := m.client.Run(cmd)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err.Error()
		}
		return tui.Red("✗ " + tui.FirstLine(msg))
	}
	return tui.Green("✓ " + okMsg)
}

func (m *model) openDetail(title, content string) {
	lines := strings.Split(strings.ReplaceAll(content, "\t", "    "), "\n")
	m.detail = &tui.Pager{Rows: lines}
	m.detailTitle = title
	m.searching = false
	m.searchQuery = ""
	m.searchHits = nil
	m.searchIdx = 0
	m.detailHoff = 0
}

// handleDetailKey drives the scrollable, searchable detail/logs overlay.
func (m *model) handleDetailKey(ev tui.Event) keyResult {
	if m.searching {
		m.handleDetailSearchKey(ev)
	} else {
		m.handleDetailNav(ev)
	}
	return keyResult{}
}

// handleDetailSearchKey edits the pager search query while a search is being
// typed.
func (m *model) handleDetailSearchKey(ev tui.Event) {
	switch ev.Key {
	case tui.KeyEnter:
		m.runSearch()
		m.searching = false
	case tui.KeyEsc:
		m.searching = false
		m.searchQuery = ""
	case tui.KeyBackspace:
		m.searchQuery = trimLastRune(m.searchQuery)
	case tui.KeyRune:
		m.searchQuery += string(ev.Rune)
	}
}

// handleDetailNav scrolls the pager, pans horizontally, and drives search jumps.
func (m *model) handleDetailNav(ev tui.Event) {
	switch {
	case ev.Key == tui.KeyUp, ev.Rune == 'k':
		m.detail.ScrollBy(-1)
	case ev.Key == tui.KeyDown, ev.Rune == 'j':
		m.detail.ScrollBy(1)
	case ev.Key == tui.KeyPgUp, ev.Key == tui.KeyCtrlU:
		m.detail.ScrollBy(-10)
	case ev.Key == tui.KeyPgDn, ev.Key == tui.KeyCtrlD:
		m.detail.ScrollBy(10)
	case ev.Rune == 'g', ev.Key == tui.KeyHome:
		m.detail.ScrollTop()
	case ev.Rune == 'G', ev.Key == tui.KeyEnd:
		m.detail.ScrollBottom()
	case ev.Key == tui.KeyLeft, ev.Rune == 'h':
		m.detailHoff -= 8
		if m.detailHoff < 0 {
			m.detailHoff = 0
		}
	case ev.Key == tui.KeyRight, ev.Rune == 'l':
		m.detailHoff += 8
	case ev.Rune == '/':
		m.searching = true
		m.searchQuery = ""
	case ev.Rune == 'n':
		m.jumpHit(1)
	case ev.Rune == 'N':
		m.jumpHit(-1)
	case ev.Key == tui.KeyEsc, ev.Rune == 'q':
		m.detail = nil
	}
}

// runSearch finds all lines containing the query and jumps to the first.
func (m *model) runSearch() {
	q := strings.ToLower(strings.TrimSpace(m.searchQuery))
	m.searchHits = nil
	m.searchIdx = 0
	if q == "" || m.detail == nil {
		return
	}
	for i, line := range m.detail.Rows {
		if strings.Contains(strings.ToLower(line), q) {
			m.searchHits = append(m.searchHits, i)
		}
	}
	if len(m.searchHits) > 0 {
		m.detail.ScrollTo(m.searchHits[0])
	}
}

// jumpHit cycles to the next/previous search match.
func (m *model) jumpHit(d int) {
	if len(m.searchHits) == 0 {
		return
	}
	m.searchIdx = (m.searchIdx + d + len(m.searchHits)) % len(m.searchHits)
	m.detail.ScrollTo(m.searchHits[m.searchIdx])
}

func trimLastRune(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return string(r[:len(r)-1])
}
