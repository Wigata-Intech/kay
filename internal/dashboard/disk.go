package dashboard

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Wigata-Intech/kay/internal/tui"
)

// diskExplorer is a du-backed drill-down over one mount: it lists the immediate
// children of a directory by size, and lets the user descend/ascend the tree.
type diskExplorer struct {
	path    string // directory currently listed
	root    string // the mount we entered from; never ascend above it
	entries []duEntry
	list    tui.List
}

// duEntry is one child directory (or file) with its apparent size in KiB.
type duEntry struct {
	kb   int64
	path string
}

// duCommand builds a one-level, single-filesystem du for path, sorted largest
// first. Sizes are KiB (-k, POSIX). The path is single-quoted so spaces and
// shell metacharacters in directory names are inert.
func duCommand(p string) string {
	return "du -x -k -d 1 -- " + shellSingleQuote(p) + " 2>/dev/null | sort -rn"
}

// shellSingleQuote wraps s in single quotes, escaping any embedded single quote,
// so it is a single inert shell word.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// parseDu turns `du -k` output into child entries sorted largest first. The row
// whose path equals base is the total for the queried directory itself and is
// dropped; only its children are returned. Lines are "<kib>\t<path>" but a
// whitespace fallback is tolerated.
func parseDu(out, base string) []duEntry {
	var es []duEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		sizeStr, p := splitDuLine(line)
		if p == "" || p == base {
			continue // blank or the base total itself
		}
		kb, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			continue
		}
		es = append(es, duEntry{kb: kb, path: p})
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].kb > es[j].kb })
	return es
}

// splitDuLine splits one du line into its size field and path. du separates them
// with a tab; if there is none (some implementations pad with spaces), fall back
// to the first whitespace run so paths keep any embedded spaces.
func splitDuLine(line string) (size, p string) {
	if before, after, found := strings.Cut(line, "\t"); found {
		return strings.TrimSpace(before), after
	}
	line = strings.TrimLeft(line, " ")
	if before, after, found := strings.Cut(line, " "); found {
		return before, strings.TrimSpace(after)
	}
	return "", ""
}

// openDiskExplorer starts a drill-down rooted at mount.
func (m *model) openDiskExplorer(mount string) {
	m.diskExpl = &diskExplorer{path: mount, root: mount}
	m.loadDiskLevel()
}

// loadDiskLevel runs du for the current directory and rebuilds the row list.
func (m *model) loadDiskLevel() {
	out, err := m.client.Run(duCommand(m.diskExpl.path))
	if err != nil {
		m.status = tui.Red("du failed: " + tui.FirstLine(err.Error()))
	}
	m.diskExpl.entries = parseDu(out, m.diskExpl.path)
	rows := make([]string, len(m.diskExpl.entries))
	for i, e := range m.diskExpl.entries {
		rows[i] = fmt.Sprintf("%10s  %s", humanKB(uint64(e.kb)), path.Base(e.path))
	}
	m.diskExpl.list.SetRows(rows)
	m.diskExpl.list.Selected = 0
}

// handleDiskExplorerKey drives the drill-down: descend into a directory, ascend
// toward (but not above) the mount, or close.
func (m *model) handleDiskExplorerKey(ev tui.Event) {
	switch {
	case ev.Key == tui.KeyEsc, ev.Rune == 'q':
		m.diskExpl = nil
	case ev.Key == tui.KeyEnter, ev.Key == tui.KeyRight, ev.Rune == 'l':
		if e, ok := m.selectedDuEntry(); ok {
			m.diskExpl.path = e.path
			m.loadDiskLevel()
		}
	case ev.Key == tui.KeyBackspace, ev.Key == tui.KeyLeft, ev.Rune == 'h':
		if parent := path.Dir(m.diskExpl.path); parent != m.diskExpl.path &&
			withinRoot(parent, m.diskExpl.root) {
			m.diskExpl.path = parent
			m.loadDiskLevel()
		}
	default:
		handleListNav(&m.diskExpl.list, ev)
	}
}

// selectedDuEntry returns the currently highlighted child, if any.
func (m *model) selectedDuEntry() (duEntry, bool) {
	i := m.diskExpl.list.Selected
	if i < 0 || i >= len(m.diskExpl.entries) {
		return duEntry{}, false
	}
	return m.diskExpl.entries[i], true
}

// withinRoot reports whether p is root or a descendant of it, so ascending never
// climbs above the mount the drill-down started from.
func withinRoot(p, root string) bool {
	if p == root {
		return true
	}
	if root == "/" {
		return true
	}
	return strings.HasPrefix(p, root+"/")
}

// renderDiskExplorer builds the drill-down view for the given inner viewport.
func (m *model) renderDiskExplorer(width, height int) (title string, body []string) {
	title = "Disk · " + m.diskExpl.path
	m.diskExpl.list.Header = fmt.Sprintf("%10s  %s", "SIZE", "NAME")
	body = m.diskExpl.list.Render(width, height)
	return title, body
}
