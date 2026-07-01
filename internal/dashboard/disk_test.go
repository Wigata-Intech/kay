// White-box: exercises the unexported du parser and diskExplorer navigation,
// which are internal to the dashboard model.
package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/Wigata-Intech/kay/internal/metrics"
	"github.com/Wigata-Intech/kay/internal/tui"
)

// pumpDu drains one async du result and applies it, standing in for the event
// loop's m.duResults arm.
func pumpDu(t *testing.T, m *model) {
	t.Helper()
	select {
	case dr := <-m.duResults:
		m.applyDu(dr)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for du result")
	}
}

func TestParseDu(t *testing.T) {
	tests := []struct {
		name string
		out  string
		base string
		want []duEntry
	}{
		{
			name: "tab separated, base dropped, sorted desc",
			out:  "100\t/var\n400\t/\n250\t/home\n50\t/usr",
			base: "/",
			want: []duEntry{{250, "/home"}, {100, "/var"}, {50, "/usr"}},
		},
		{
			name: "space-separated fallback",
			out:  "300   /srv\n900   /data",
			base: "/mnt",
			want: []duEntry{{900, "/data"}, {300, "/srv"}},
		},
		{
			name: "path with spaces preserved (tab split)",
			out:  "12\t/home/my docs\n40\t/opt",
			base: "/",
			want: []duEntry{{40, "/opt"}, {12, "/home/my docs"}},
		},
		{
			name: "garbage and blank lines skipped",
			out:  "\nnotanumber\t/x\n\n64\t/ok\n",
			base: "/",
			want: []duEntry{{64, "/ok"}},
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
			got := parseDu(tt.out, tt.base)
			if len(got) != len(tt.want) {
				t.Fatalf("parseDu = %v, want %v", got, tt.want)
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

func TestDuCommand(t *testing.T) {
	got := duCommand("/var/log")
	for _, want := range []string{"du -x -k -d 1 --", "'/var/log'", "sort -rn"} {
		if !strings.Contains(got, want) {
			t.Errorf("duCommand missing %q: %q", want, got)
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
	du := "400\t/\n250\t/home\n100\t/var"

	t.Run("enter on a mount opens the explorer", func(t *testing.T) {
		m := newModel()
		m.client = &fakeClient{out: du}
		m.duResults = make(chan duResult, 1)
		m.tab = tabDisk
		m.disk.Selected = 0 // sampleSnap has one disk mounted at "/"

		m.diskAction(tui.Event{Key: tui.KeyEnter})
		if m.diskExpl == nil {
			t.Fatal("Enter should open the disk explorer")
		}
		if m.diskExpl.path != "/" || m.diskExpl.root != "/" {
			t.Errorf("explorer at %q (root %q), want /", m.diskExpl.path, m.diskExpl.root)
		}
		if !m.diskExpl.loading {
			t.Error("explorer should start in the loading state")
		}
		pumpDu(t, m)
		if m.diskExpl.loading {
			t.Error("loading should clear after the scan result")
		}
		if len(m.diskExpl.entries) != 2 {
			t.Fatalf("entries = %d, want 2 (children of /)", len(m.diskExpl.entries))
		}
		if m.diskExpl.entries[0].path != "/home" {
			t.Errorf("first (largest) entry = %q, want /home", m.diskExpl.entries[0].path)
		}
	})

	t.Run("non-enter key is ignored", func(t *testing.T) {
		m := newModel()
		m.client = &fakeClient{out: du}
		m.tab = tabDisk
		m.diskAction(tui.Event{Rune: 'j'})
		if m.diskExpl != nil {
			t.Error("only Enter should open the explorer")
		}
	})

	t.Run("no disks: enter is a no-op", func(t *testing.T) {
		m := newModel()
		m.client = &fakeClient{out: du}
		m.snap.Disks = nil
		m.disk.SetRows(nil)
		m.tab = tabDisk
		m.diskAction(tui.Event{Key: tui.KeyEnter})
		if m.diskExpl != nil {
			t.Error("no disks should not open the explorer")
		}
	})
}

func TestDiskExplorerLoadingIgnoresKeys(t *testing.T) {
	m := newModel()
	m.client = &fakeClient{out: "400\t/\n100\t/var"}
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

func TestDiskExplorerNavigation(t *testing.T) {
	m := newModel()
	m.snap.Disks = []metrics.Disk{{Mount: "/data"}}
	m.client = &fakeClient{out: "400\t/data\n250\t/data/logs\n100\t/data/cache"}
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
