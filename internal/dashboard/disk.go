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
	path       string    // directory currently listed
	root       string    // the mount we entered from; never ascend above it
	entries    []duEntry // every child of path (dirs + files), largest first
	visible    []duEntry // entries currently shown (hidden filtered unless showHidden)
	list       tui.List
	loading    bool
	pending    string // directory whose scan is in flight (matched in applyDu)
	showHidden bool   // include dotfiles (toggled with '.')
}

// duEntry is one child of the current directory: a directory (recursive size via
// du) or a file (size via find). Sizes are in KiB.
type duEntry struct {
	kb     int64
	path   string
	isDir  bool
	hidden bool // basename begins with '.'
}

// listingCommand builds a one-level listing of p: directories with their
// recursive du size and files with their size, each tagged d/f. The path is
// single-quoted so spaces and shell metacharacters in names are inert. Files use
// GNU find's -printf; if find lacks it, only directories appear.
func listingCommand(p string) string {
	q := shellSingleQuote(p)
	return "{ du -x -k -d 1 -- " + q + " 2>/dev/null | awk -v OFS='\\t' '{print \"d\",$0}'; " +
		"find " + q + " -maxdepth 1 -mindepth 1 -type f -printf 'f\\t%k\\t%p\\n' 2>/dev/null; }"
}

// shellSingleQuote wraps s in single quotes, escaping any embedded single quote,
// so it is a single inert shell word.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// parseListing turns the tagged du+find output into child entries sorted largest
// first. Each line is "<type>\t<kib>\t<path>" where type is d (directory) or f
// (file). The directory row whose path equals base is the queried directory's own
// total and is dropped; only its children are returned.
func parseListing(out, base string) []duEntry {
	var es []duEntry
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		typ, sizeStr, p := parts[0], strings.TrimSpace(parts[1]), parts[2]
		isDir := typ == "d"
		if p == "" || (isDir && p == base) {
			continue // blank or the base total itself
		}
		kb, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			continue
		}
		es = append(es, duEntry{
			kb:     kb,
			path:   p,
			isDir:  isDir,
			hidden: strings.HasPrefix(path.Base(p), "."),
		})
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].kb > es[j].kb })
	return es
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
		out, err := cl.Run(listingCommand(p))
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
	m.diskExpl.entries = parseListing(dr.out, dr.path)
	m.rebuildDiskRows()
}

// rebuildDiskRows recomputes the visible entries (hidden filtered unless
// showHidden) and the formatted list rows. Directories get a trailing slash;
// hidden entries are dimmed and tagged so the user can tell they are dotfiles.
func (m *model) rebuildDiskRows() {
	de := m.diskExpl
	de.visible = de.visible[:0]
	var rows []string
	for _, e := range de.entries {
		if e.hidden && !de.showHidden {
			continue
		}
		de.visible = append(de.visible, e)
		name := path.Base(e.path)
		if e.isDir {
			name += "/"
		}
		row := fmt.Sprintf("%10s  %s", humanKB(uint64(e.kb)), name)
		if e.hidden {
			row = tui.Dim(row + "  (hidden)")
		}
		rows = append(rows, row)
	}
	de.list.SetRows(rows)
	de.list.Selected = 0
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
	case ev.Rune == '.':
		m.diskExpl.showHidden = !m.diskExpl.showHidden
		m.rebuildDiskRows()
	case ev.Key == tui.KeyEnter, ev.Key == tui.KeyRight, ev.Rune == 'l':
		if e, ok := m.selectedDuEntry(); ok {
			if e.isDir {
				m.startDuLoad(e.path)
			} else {
				m.notice = fmt.Sprintf("can't open %q — file preview isn't supported yet", path.Base(e.path))
			}
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

// selectedDuEntry returns the currently highlighted visible child, if any.
func (m *model) selectedDuEntry() (duEntry, bool) {
	i := m.diskExpl.list.Selected
	if i < 0 || i >= len(m.diskExpl.visible) {
		return duEntry{}, false
	}
	return m.diskExpl.visible[i], true
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
