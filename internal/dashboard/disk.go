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
// Scans run asynchronously (du can be slow), so loading gates input while one is
// in flight and pending records the directory being scanned.
type diskExplorer struct {
	path    string // directory currently listed
	root    string // the mount we entered from; never ascend above it
	entries []duEntry
	list    tui.List
	loading bool
	pending string // directory whose scan is in flight (matched in applyDu)
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

// openDiskExplorer starts a drill-down rooted at mount and kicks off its scan.
func (m *model) openDiskExplorer(mount string) {
	m.diskExpl = &diskExplorer{path: mount, root: mount}
	m.startDuLoad(mount)
}

// startDuLoad runs du for p in a goroutine so the event loop never blocks (du can
// take seconds on large trees). The result is delivered on m.duResults and
// applied by applyDu. Only one scan is in flight at a time, gated by loading.
func (m *model) startDuLoad(p string) {
	m.diskExpl.loading = true
	m.diskExpl.pending = p
	cl := m.client // snapshot: reconnect may replace m.client later
	ch := m.duResults
	go func() {
		out, err := cl.Run(duCommand(p))
		ch <- duResult{path: p, out: out, err: err}
	}()
}

// applyDu installs a completed scan. It ignores stale results (the explorer was
// closed, or the user navigated elsewhere) by matching the pending path.
func (m *model) applyDu(dr duResult) {
	if m.diskExpl == nil || !m.diskExpl.loading || dr.path != m.diskExpl.pending {
		return
	}
	m.diskExpl.loading = false
	m.diskExpl.pending = ""
	if dr.err != nil {
		m.status = tui.Red("du failed: " + tui.FirstLine(dr.err.Error()))
	}
	m.diskExpl.path = dr.path
	m.diskExpl.entries = parseDu(dr.out, dr.path)
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
	// While a scan is in flight, ignore everything except a force-close so queued
	// keystrokes don't stack up and fire when the (possibly slow) du returns.
	if m.diskExpl.loading {
		if ev.Key == tui.KeyEsc || ev.Rune == 'q' {
			m.diskExpl = nil
		}
		return
	}

	switch {
	case ev.Key == tui.KeyEsc, ev.Rune == 'q':
		m.diskExpl = nil
	case ev.Key == tui.KeyEnter, ev.Key == tui.KeyRight, ev.Rune == 'l':
		if e, ok := m.selectedDuEntry(); ok {
			m.startDuLoad(e.path)
		}
	case ev.Key == tui.KeyBackspace, ev.Key == tui.KeyLeft, ev.Rune == 'h':
		if parent := path.Dir(m.diskExpl.path); parent != m.diskExpl.path &&
			withinRoot(parent, m.diskExpl.root) {
			m.startDuLoad(parent)
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
// While a scan is in flight it shows a scanning notice instead of the list.
func (m *model) renderDiskExplorer(width, height int) (title string, body []string) {
	if m.diskExpl.loading {
		title = "Disk · scanning…"
		return title, []string{"", tui.Dim("  scanning " + m.diskExpl.pending + " … (esc to cancel)")}
	}
	title = "Disk · " + m.diskExpl.path
	m.diskExpl.list.Header = fmt.Sprintf("%10s  %s", "SIZE", "NAME")
	body = m.diskExpl.list.Render(width, height)
	return title, body
}
