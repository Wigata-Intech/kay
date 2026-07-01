// Command membench measures the steady-state residency of holding N long-lived
// SSH connections open — the Tier-2 "what does a persistent connection cost"
// question. It dials N connections to the configured target, holds them, and
// reports heap and goroutine deltas, so per-connection cost is (after − before)/N.
//
// All the pool libraries hold an *ssh.Client underneath, so this per-connection
// residency is representative across strategies; it is measured here on the raw
// client to keep the number clean.
//
// Run:
//
//	BENCH_SSH_ADDR=127.0.0.1:22 BENCH_SSH_USER=$USER BENCH_SSH_KEY=~/.ssh/id_ed25519 \
//	  go run ./membench -n 50 -hold 5s
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	n := flag.Int("n", 50, "number of connections to hold open")
	hold := flag.Duration("hold", 5*time.Second, "how long to hold them (observe CPU externally with top/pprof)")
	flag.Parse()

	addr, user, key := os.Getenv("BENCH_SSH_ADDR"), os.Getenv("BENCH_SSH_USER"), os.Getenv("BENCH_SSH_KEY")
	if addr == "" || user == "" || key == "" {
		fmt.Fprintln(os.Stderr, "set BENCH_SSH_ADDR, BENCH_SSH_USER, BENCH_SSH_KEY")
		os.Exit(2)
	}
	cfg, err := clientConfig(user, key)
	if err != nil {
		fatal(err)
	}

	runtime.GC()
	before := heap()
	gBefore := runtime.NumGoroutine()

	clients := make([]*ssh.Client, 0, *n)
	for i := 0; i < *n; i++ {
		c, err := ssh.Dial("tcp", addr, cfg)
		if err != nil {
			fatal(fmt.Errorf("dial %d: %w", i, err))
		}
		clients = append(clients, c)
	}

	runtime.GC()
	after := heap()
	gAfter := runtime.NumGoroutine()

	perConn := float64(after-before) / float64(*n)
	fmt.Printf("connections:        %d\n", *n)
	fmt.Printf("heap in use:        %s -> %s  (Δ %s)\n", human(before), human(after), human(after-before))
	fmt.Printf("per connection:     %.1f KiB\n", perConn/1024)
	fmt.Printf("goroutines:         %d -> %d  (%.1f per connection)\n", gBefore, gAfter, float64(gAfter-gBefore)/float64(*n))
	fmt.Printf("holding %s (idle CPU should be ~0; watch with `top -pid %d`)...\n", *hold, os.Getpid())

	time.Sleep(*hold)
	for _, c := range clients {
		_ = c.Close()
	}
}

func clientConfig(user, keyPath string) (*ssh.ClientConfig, error) {
	pem, err := os.ReadFile(keyPath) //nolint:gosec // operator-supplied key path in a bench harness
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		return nil, err
	}
	return &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}, nil
}

func heap() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}

func human(b uint64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%.1f KiB", float64(b)/(1<<10))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "membench:", err)
	os.Exit(1)
}
