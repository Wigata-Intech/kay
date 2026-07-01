// White-box: exercises the unexported du/find parser and diskExplorer
// navigation (files, hidden toggle, file-open notice), which are internal to the
// dashboard model.
package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// pumpDu drains one async listing result and applies it, standing in for the
// event loop's m.duResults arm.
func pumpDu(t *testing.T, m *model) {
	t.Helper()
	select {
	case dr := <-m.duResults:
		m.applyDu(dr)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for du result")
	}
}

func TestParseListing(t *testing.T) {
	tests := []struct {
		name string
		out  string
		base string
		want []duEntry
	}{
		{
			name: "dirs and files, base dropped, sorted, hidden flagged",
			out:  "d\t400\t/\nd\t250\t/home\nf\t100\t/setup.sh\nd\t10\t/.cache",
			base: "/",
			want: []duEntry{
				{kb: 250, path: "/home", isDir: true},
				{kb: 100, path: "/setup.sh"},
				{kb: 10, path: "/.cache", isDir: true, hidden: true},
			},
		},
		{
			name: "hidden file flagged",
			out:  "f\t8\t/app/.env\nf\t20\t/app/main.go",
			base: "/app",
			want: []duEntry{
				{kb: 20, path: "/app/main.go"},
				{kb: 8, path: "/app/.env", hidden: true},
			},
		},
		{
			name: "garbage and short lines skipped",
			out:  "\nd\tnotanum\t/x\nonly\ttwo\nf\t64\t/ok",
			base: "/",
			want: []duEntry{{kb: 64, path: "/ok"}},
		},
		{
			name: "empty output",
			out:  "",
			base: "/",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseListing(tt.out, tt.base)
			if len(got) != len(tt.want) {
				t.Fatalf("parseListing = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestShellSingleQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "/var/log", want: "'/var/log'"},
		{name: "with space", in: "/my dir", want: "'/my dir'"},
		{name: "embedded single quote", in: "/a'b", want: `'/a'\''b'`},
		{name: "shell metachars inert", in: "/x; rm -rf /", want: "'/x; rm -rf /'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellSingleQuote(tt.in); got != tt.want {
				t.Errorf("shellSingleQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestListingCommand(t *testing.T) {
	got := listingCommand("/var/log")
	for _, want := range []string{"du -x -k -d 1 --", "'/var/log'", "find", "-type f", "-printf"} {
		if !strings.Contains(got, want) {
			t.Errorf("listingCommand missing %q: %q", want, got)
		}
	}
}

func TestWithinRoot(t *testing.T) {
	tests := []struct {
		name string
		p    string
		root string
		want bool
	}{
		{name: "equal", p: "/data", root: "/data", want: true},
		{name: "descendant", p: "/data/logs", root: "/data", want: true},
		{name: "root slash allows anything", p: "/anything", root: "/", want: true},
		{name: "sibling rejected", p: "/other", root: "/data", want: false},
		{name: "prefix-but-not-child rejected", p: "/data2", root: "/data", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := withinRoot(tt.p, tt.root); got != tt.want {
				t.Errorf("withinRoot(%q, %q) = %v, want %v", tt.p, tt.root, got, tt.want)
			}
		})
	}
}

func TestDiskAction(t *testing.T) {
	listing := "d\t400\t/\nd\t250\t/home\nd\t100\t/var"

	t.Run("enter on a mount opens the explorer", func(t *testing.T) {
		m := newModel()
		m.client = &fakeClient{out: listing}
		m.duResults = make(chan duResult, 1)
		m.tab = tabDisk
		m.disk.Selected = 0 // sampleSnap has one disk mounted at "/"

		m.diskAction(tui.Event{Key: tui.KeyEnter})
		if m.diskExpl == nil {
			t.Fatal("Enter should open the disk explorer")
		}
		if !m.diskExpl.loading {
			t.Error("explorer should start in the loading state")
		}
		pumpDu(t, m)
		if m.diskExpl.loading {
			t.Error("loading should clear after the scan result")
		}
		if len(m.diskExpl.visible) != 2 {
			t.Fatalf("visible = %d, want 2 (children of /)", len(m.diskExpl.visible))
		}
		if m.diskExpl.visible[0].path != "/home" {
			t.Errorf("first (largest) entry = %q, want /home", m.diskExpl.visible[0].path)
		}
	})

	t.Run("non-enter key is ignored", func(t *testing.T) {
		m := newModel()
		m.client = &fakeClient{out: listing}
		m.tab = tabDisk
		m.diskAction(tui.Event{Rune: 'j'})
		if m.diskExpl != nil {
			t.Error("only Enter should open the explorer")
		}
	})

	t.Run("no disks: enter is a no-op", func(t *testing.T) {
		m := newModel()
		m.client = &fakeClient{out: listing}
		m.snap.Disks = nil
		m.disk.SetRows(nil)
		m.tab = tabDisk
		m.diskAction(tui.Event{Key: tui.KeyEnter})
		if m.diskExpl != nil {
			t.Error("no disks should not open the explorer")
		}
	})
}

func TestDiskExplorerNavigation(t *testing.T) {
	m := newModel()
	m.snap.Disks = []metrics.Disk{{Mount: "/data"}}
	m.client = &fakeClient{out: "d\t400\t/data\nd\t250\t/data/logs\nd\t100\t/data/cache"}
	m.duResults = make(chan duResult, 1)
	m.openDiskExplorer("/data")
	pumpDu(t, m) // initial scan completes

	// Descend into the highlighted child (largest first => /data/logs).
	m.handleDiskExplorerKey(tui.Event{Key: tui.KeyEnter})
	pumpDu(t, m)
	if m.diskExpl.path != "/data/logs" {
		t.Fatalf("after Enter, path = %q, want /data/logs", m.diskExpl.path)
	}

	// Ascend back to the root.
	m.handleDiskExplorerKey(tui.Event{Key: tui.KeyBackspace})
	pumpDu(t, m)
	if m.diskExpl.path != "/data" {
		t.Fatalf("after Backspace, path = %q, want /data", m.diskExpl.path)
	}

	// Ascending at the root must not climb above the mount (no scan is started).
	m.handleDiskExplorerKey(tui.Event{Key: tui.KeyBackspace})
	if m.diskExpl.path != "/data" {
		t.Errorf("Backspace at root changed path to %q, want /data (pinned)", m.diskExpl.path)
	}
	if m.diskExpl.loading {
		t.Error("pinned Backspace should not start a scan")
	}

	// Esc closes the explorer.
	m.handleDiskExplorerKey(tui.Event{Key: tui.KeyEsc})
	if m.diskExpl != nil {
		t.Error("Esc should close the explorer")
	}
}

func TestDiskHiddenToggle(t *testing.T) {
	m := newModel()
	m.snap.Disks = []metrics.Disk{{Mount: "/"}}
	m.client = &fakeClient{out: "d\t400\t/\nd\t50\t/etc\nd\t10\t/.git"}
	m.duResults = make(chan duResult, 1)
	m.openDiskExplorer("/")
	pumpDu(t, m)

	// Dotfiles are hidden by default: only /etc is visible.
	if len(m.diskExpl.visible) != 1 || m.diskExpl.visible[0].path != "/etc" {
		t.Fatalf("default visible = %+v, want just /etc", m.diskExpl.visible)
	}

	// '.' reveals hidden entries.
	m.handleDiskExplorerKey(tui.Event{Rune: '.'})
	if len(m.diskExpl.visible) != 2 {
		t.Fatalf("after toggle, visible = %d, want 2", len(m.diskExpl.visible))
	}

	// '.' again hides them.
	m.handleDiskExplorerKey(tui.Event{Rune: '.'})
	if len(m.diskExpl.visible) != 1 {
		t.Errorf("after second toggle, visible = %d, want 1", len(m.diskExpl.visible))
	}
}

func TestDiskOpenFileShowsNotice(t *testing.T) {
	m := newModel()
	m.snap.Disks = []metrics.Disk{{Mount: "/app"}}
	m.client = &fakeClient{out: "f\t20\t/app/main.go"}
	m.duResults = make(chan duResult, 1)
	m.openDiskExplorer("/app")
	pumpDu(t, m)

	// Opening a file (not a directory) raises the modal notice, not a scan.
	m.handleDiskExplorerKey(tui.Event{Key: tui.KeyEnter})
	if m.notice == "" || !strings.Contains(m.notice, "main.go") {
		t.Fatalf("notice = %q, want a file-not-supported message", m.notice)
	}
	if m.diskExpl.loading {
		t.Error("opening a file should not start a scan")
	}

	// Any key dismisses the notice.
	m.handleKey(tui.Event{Rune: 'x'})
	if m.notice != "" {
		t.Errorf("notice should be dismissed, got %q", m.notice)
	}
}

func TestDiskExplorerLoadingIgnoresKeys(t *testing.T) {
	m := newModel()
	m.client = &fakeClient{out: "d\t400\t/\nd\t100\t/var"}
	m.duResults = make(chan duResult, 1)
	m.openDiskExplorer("/") // now loading, scan in flight

	// Navigation keys are ignored while loading.
	m.handleDiskExplorerKey(tui.Event{Key: tui.KeyEnter})
	m.handleDiskExplorerKey(tui.Event{Key: tui.KeyDown})
	if m.diskExpl == nil || !m.diskExpl.loading {
		t.Fatal("keys other than close must be ignored while loading")
	}
	// Esc force-closes even mid-scan.
	m.handleDiskExplorerKey(tui.Event{Key: tui.KeyEsc})
	if m.diskExpl != nil {
		t.Error("Esc should force-close during loading")
	}
	// Drain the now-stale in-flight result: applyDu must ignore it (explorer nil).
	pumpDu(t, m)
	if m.diskExpl != nil {
		t.Error("a stale scan result must not reopen the explorer")
	}
}

func TestVimTabSwitch(t *testing.T) {
	m := newModel()
	m.tab = tabOverview

	m.handleKey(tui.Event{Rune: 'L'}) // next tab
	if m.tab != tabProcesses {
		t.Errorf("after L, tab = %d, want %d", m.tab, tabProcesses)
	}
	m.handleKey(tui.Event{Rune: 'H'}) // previous tab
	if m.tab != tabOverview {
		t.Errorf("after H, tab = %d, want %d", m.tab, tabOverview)
	}
}
