package metrics

import (
	"math"
	"testing"
)

// fixture mirrors the marker-delimited shape produced by remoteScript, using
// controlled numbers so expected results are easy to verify.
const fixture = `@@HOST
web-1
@@UPTIME
123456.78 987654.00
@@CPU1
cpu  100 0 100 800 0 0 0 0 0 0
@@CPU2
cpu  150 0 150 900 0 0 0 0 0 0
@@MEM
MemTotal:        4000000 kB
MemAvailable:    1000000 kB
@@LOAD
0.50 0.40 0.30 2/345 6789
@@NCPU
4
@@NET
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:    1000      10    0    0    0     0          0         0     1000      10    0    0    0     0       0          0
  eth0:  500000     400    0    0    0     0          0         0   250000     300    0    0    0     0       0          0
@@DISK
/dev/sda1|4000000000|1600000000|/
/dev/sda2|1000000000|500000000|/data
proc|0|0|/proc
@@PROC
999|90.0|12.5|my proc
1|0.5|0.1|systemd
200|5.0|3.0|redis-server
@@DOCKER
PRESENT
abc123|web|nginx:latest|Up 2 hours
def456|db|postgres:16|Up 5 minutes
@@END
`

func approx(a, b float64) bool { return math.Abs(a-b) < 0.001 }

func TestParseFixture(t *testing.T) {
	s, err := Parse(fixture)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	t.Run("identity", func(t *testing.T) {
		if s.Hostname != "web-1" {
			t.Errorf("hostname = %q", s.Hostname)
		}
		if !approx(s.UptimeSec, 123456.78) {
			t.Errorf("uptime = %v", s.UptimeSec)
		}
		if s.NumCPU != 4 {
			t.Errorf("numcpu = %d", s.NumCPU)
		}
	})

	t.Run("cpu_load_mem", func(t *testing.T) {
		if !approx(s.CPUPercent, 50.0) {
			t.Errorf("cpu%% = %v, want 50", s.CPUPercent)
		}
		if !approx(s.Load1, 0.5) || !approx(s.Load5, 0.4) || !approx(s.Load15, 0.3) {
			t.Errorf("load = %v %v %v", s.Load1, s.Load5, s.Load15)
		}
		if !approx(s.MemUsedPercent, 75.0) {
			t.Errorf("mem%% = %v", s.MemUsedPercent)
		}
	})

	t.Run("net", func(t *testing.T) {
		var eth0 *NetIface
		for i := range s.Net {
			if s.Net[i].Name == "eth0" {
				eth0 = &s.Net[i]
			}
		}
		if eth0 == nil || eth0.RxBytes != 500000 || eth0.TxBytes != 250000 {
			t.Errorf("eth0 = %+v", eth0)
		}
	})

	t.Run("disk", func(t *testing.T) {
		// /proc excluded; root present and 40% used.
		if len(s.Disks) != 2 {
			t.Fatalf("disks = %d (want 2, /proc excluded)", len(s.Disks))
		}
		root, ok := s.RootDisk()
		if !ok || root.Mount != "/" || !approx(root.UsedPercent(), 40.0) {
			t.Errorf("root disk = %+v used%%=%v", root, root.UsedPercent())
		}
	})

	t.Run("procs", func(t *testing.T) {
		// sorted by CPU desc; name with a space preserved.
		if len(s.Procs) != 3 {
			t.Fatalf("procs = %d", len(s.Procs))
		}
		if s.Procs[0].PID != 999 || s.Procs[0].Name != "my proc" ||
			!approx(s.Procs[0].CPU, 90.0) || !approx(s.Procs[0].Mem, 12.5) {
			t.Errorf("top proc = %+v", s.Procs[0])
		}
	})

	t.Run("docker", func(t *testing.T) {
		if !s.DockerPresent || len(s.Docker) != 2 ||
			s.Docker[0].Name != "web" || s.Docker[0].Image != "nginx:latest" {
			t.Errorf("docker = %+v present=%v", s.Docker, s.DockerPresent)
		}
	})
}

func TestParseDockerAbsent(t *testing.T) {
	s, _ := Parse("@@DOCKER\nABSENT\n@@END\n")
	if s.DockerPresent || len(s.Docker) != 0 {
		t.Errorf("expected docker absent, got present=%v n=%d", s.DockerPresent, len(s.Docker))
	}
}

type fakeRunner struct{ out string }

func (f fakeRunner) Run(string) (string, error) { return f.out, nil }

func TestCollectViaRunner(t *testing.T) {
	s, err := Collect(fakeRunner{out: fixture})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if s.Hostname != "web-1" {
		t.Errorf("hostname = %q", s.Hostname)
	}
}
