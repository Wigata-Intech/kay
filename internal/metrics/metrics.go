// Package metrics collects a snapshot of a remote Linux host's vitals by
// running a single batched command over SSH and parsing /proc and command
// output. No agent is installed on the remote.
package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Runner is the minimal SSH capability metrics needs (satisfied by *sshx.Client).
type Runner interface {
	Run(cmd string) (string, error)
}

// NetIface holds cumulative byte counters for one interface.
type NetIface struct {
	Name             string
	RxBytes, TxBytes uint64
}

// Proc is one process row (CPU is instantaneous when sourced from top).
type Proc struct {
	PID  int
	Name string
	CPU  float64
	Mem  float64
}

// Container is one Docker container.
type Container struct {
	ID, Name, Image, Status string
}

// Disk is usage for one mounted filesystem.
type Disk struct {
	Filesystem string
	Mount      string
	TotalBytes uint64
	UsedBytes  uint64
}

// UsedPercent returns disk utilisation 0..100.
func (d Disk) UsedPercent() float64 {
	if d.TotalBytes == 0 {
		return 0
	}
	return float64(d.UsedBytes) / float64(d.TotalBytes) * 100
}

// Snapshot is a single point-in-time reading.
type Snapshot struct {
	Hostname       string
	UptimeSec      float64
	NumCPU         int
	CPUPercent     float64
	Load1          float64
	Load5          float64
	Load15         float64
	MemTotalKB     uint64
	MemAvailableKB uint64
	MemUsedPercent float64
	Net            []NetIface
	Procs          []Proc
	Disks          []Disk
	Docker         []Container
	DockerPresent  bool
}

// RootDisk returns the filesystem mounted at "/", or the largest one.
func (s Snapshot) RootDisk() (Disk, bool) {
	var best Disk
	found := false
	for _, d := range s.Disks {
		if d.Mount == "/" {
			return d, true
		}
		if d.TotalBytes > best.TotalBytes {
			best, found = d, true
		}
	}
	return best, found
}

// remoteScript reads everything in one shot. Two /proc/stat samples bracket a
// short sleep so host CPU utilisation can be derived from the delta; per-process
// CPU comes from `top -bn1` (instantaneous), with a `ps` fallback.
const remoteScript = `
echo '@@HOST'; hostname
echo '@@UPTIME'; cat /proc/uptime
echo '@@CPU1'; head -1 /proc/stat
sleep 0.25
echo '@@CPU2'; head -1 /proc/stat
echo '@@MEM'; grep -E '^(MemTotal|MemAvailable):' /proc/meminfo
echo '@@LOAD'; cat /proc/loadavg
echo '@@NCPU'; nproc
echo '@@NET'; cat /proc/net/dev
echo '@@DISK'; df -P -B1 -x tmpfs -x devtmpfs -x overlay -x squashfs 2>/dev/null | awk 'NR>1{print $1"|"$2"|"$3"|"$6}'
echo '@@PROC'
if command -v top >/dev/null 2>&1; then
  top -bn1 -w 512 2>/dev/null | awk 'p&&$1~/^[0-9]+$/{c=$12;for(i=13;i<=NF;i++)c=c" "$i;print $1"|"$9"|"$10"|"c} /^[[:space:]]*PID/{p=1}' | head -20
else
  ps -eo pid,pcpu,pmem,comm --sort=-pcpu --no-headers 2>/dev/null | awk '{print $1"|"$2"|"$3"|"$4}' | head -20
fi
echo '@@DOCKER'; if command -v docker >/dev/null 2>&1; then echo PRESENT; docker ps --format '{{.ID}}|{{.Names}}|{{.Image}}|{{.Status}}' 2>/dev/null; else echo ABSENT; fi
echo '@@END'
`

// Collect runs the batched script and parses the result.
func Collect(r Runner) (Snapshot, error) {
	out, err := r.Run(remoteScript)
	if err != nil && strings.TrimSpace(out) == "" {
		return Snapshot{}, fmt.Errorf("metrics command failed: %w", err)
	}
	return Parse(out)
}

// Parse decodes the marker-delimited output into a Snapshot.
func Parse(out string) (Snapshot, error) {
	sec := split(out)
	var s Snapshot

	s.Hostname = strings.TrimSpace(sec["HOST"])
	if f := strings.Fields(sec["UPTIME"]); len(f) > 0 {
		s.UptimeSec, _ = strconv.ParseFloat(f[0], 64)
	}
	s.NumCPU, _ = strconv.Atoi(strings.TrimSpace(sec["NCPU"]))
	if s.NumCPU == 0 {
		s.NumCPU = 1
	}
	s.CPUPercent = cpuPercent(sec["CPU1"], sec["CPU2"])
	s.Load1, s.Load5, s.Load15 = parseLoad(sec["LOAD"])
	s.MemTotalKB, s.MemAvailableKB = parseMem(sec["MEM"])
	if s.MemTotalKB > 0 {
		s.MemUsedPercent = float64(s.MemTotalKB-s.MemAvailableKB) / float64(s.MemTotalKB) * 100
	}
	s.Net = parseNet(sec["NET"])
	s.Disks = parseDisk(sec["DISK"])
	s.Procs = parseProcs(sec["PROC"])
	s.Docker, s.DockerPresent = parseDocker(sec["DOCKER"])
	return s, nil
}

func split(out string) map[string]string {
	sec := map[string]string{}
	var cur string
	var b strings.Builder
	flush := func() {
		if cur != "" {
			sec[cur] = b.String()
		}
		b.Reset()
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "@@") {
			flush()
			cur = strings.TrimPrefix(line, "@@")
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	return sec
}

func cpuStat(line string) (total, idle uint64) {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0
	}
	var vals []uint64
	for _, x := range f[1:] {
		v, err := strconv.ParseUint(x, 10, 64)
		if err != nil {
			break
		}
		vals = append(vals, v)
	}
	for _, v := range vals {
		total += v
	}
	if len(vals) >= 5 {
		idle = vals[3] + vals[4]
	} else if len(vals) >= 4 {
		idle = vals[3]
	}
	return total, idle
}

func cpuPercent(a, b string) float64 {
	t1, i1 := cpuStat(strings.TrimSpace(a))
	t2, i2 := cpuStat(strings.TrimSpace(b))
	dt := float64(t2) - float64(t1)
	di := float64(i2) - float64(i1)
	if dt <= 0 {
		return 0
	}
	return clampPct((dt - di) / dt * 100)
}

func clampPct(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

func parseLoad(s string) (l1, l5, l15 float64) {
	f := strings.Fields(s)
	if len(f) >= 3 {
		l1, _ = strconv.ParseFloat(f[0], 64)
		l5, _ = strconv.ParseFloat(f[1], 64)
		l15, _ = strconv.ParseFloat(f[2], 64)
	}
	return
}

func parseMem(s string) (total, avail uint64) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = v
		case "MemAvailable:":
			avail = v
		}
	}
	return
}

func parseNet(s string) []NetIface {
	var out []NetIface
	for _, line := range strings.Split(s, "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		name := strings.TrimSpace(parts[0])
		if name == "" || name == "face" {
			continue
		}
		f := strings.Fields(parts[1])
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseUint(f[0], 10, 64)
		tx, _ := strconv.ParseUint(f[8], 10, 64)
		out = append(out, NetIface{Name: name, RxBytes: rx, TxBytes: tx})
	}
	return out
}

func parseDisk(s string) []Disk {
	var out []Disk
	for _, line := range strings.Split(s, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 4 {
			continue
		}
		mount := parts[3]
		// Skip pseudo / boot mounts that aren't interesting.
		if strings.HasPrefix(mount, "/proc") || strings.HasPrefix(mount, "/sys") ||
			strings.HasPrefix(mount, "/run") || strings.HasPrefix(mount, "/dev") {
			continue
		}
		total, _ := strconv.ParseUint(parts[1], 10, 64)
		used, _ := strconv.ParseUint(parts[2], 10, 64)
		if total == 0 {
			continue
		}
		out = append(out, Disk{Filesystem: parts[0], Mount: mount, TotalBytes: total, UsedBytes: used})
	}
	return out
}

func parseProcs(s string) []Proc {
	var out []Proc
	for _, line := range strings.Split(s, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 4 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		cpu, _ := strconv.ParseFloat(parts[1], 64)
		mem, _ := strconv.ParseFloat(parts[2], 64)
		out = append(out, Proc{PID: pid, CPU: cpu, Mem: mem, Name: parts[3]})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CPU > out[j].CPU })
	return out
}

func parseDocker(s string) ([]Container, bool) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	present := false
	var out []Container
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch line {
		case "":
			continue
		case "PRESENT":
			present = true
			continue
		case "ABSENT":
			return nil, false
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) == 4 {
			out = append(out, Container{ID: parts[0], Name: parts[1], Image: parts[2], Status: parts[3]})
		}
	}
	return out, present
}
