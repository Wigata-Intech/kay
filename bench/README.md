# kay SSH connection benchmarks

A **separate Go module** (`github.com/Wigata-Intech/kay-bench`, its own `go.mod`)
so the rival pool libraries it imports never touch kay's dependency set. kay's own
module (`../go.mod`) stays x/crypto-only; `make ci` at the repo root does not see
this directory.

These are the **Tier 2 / Tier 3** benchmarks from the design doc
(`second-brain/docs/technical-design/[4]ssh-connection-pool.md`). They need a
**real sshd**, so they are skipped unless a target is configured. (Tier 1, the
in-repo micro-benchmarks of the pool's own dispatch overhead, lives in
`../internal/sshx/pool_bench_test.go`.)

## What it compares

Connection-reuse strategies, all running the same trivial remote command (`true`)
to isolate transport/session overhead from the command:

| Strategy | What it is |
|----------|------------|
| **Kay-style reuse** | one `*ssh.Client`, a fresh `Session` per op — mirrors `internal/sshx.Client.Run` (which `internal/` visibility forbids importing here) |
| **desops/sshpool** | closest rival: per-host pool, `ExecCombinedOutputString` per op |
| **jolestar/go-commons-pool** | generic object pool holding `*ssh.Client`: Borrow → Session → Return (the "wrong abstraction" — conn-per-borrow) |
| **dial-per-op** | full dial+handshake every op — what kay's fleet did before 0.2.0; the reference the reuse strategies must beat |

## Configure the target

```sh
export BENCH_SSH_ADDR=127.0.0.1:22          # host:port
export BENCH_SSH_USER=$USER
export BENCH_SSH_KEY=~/.ssh/id_ed25519      # a private key authorized on the target
```

Against your own machine (no remote box needed): generate a throwaway key, add its
public half to `~/.ssh/authorized_keys`, and point `BENCH_SSH_*` at `127.0.0.1:22`.

## Run

**Tier 2 — connect latency & warm throughput** (`compare_test.go`):

```sh
go test -bench=. -benchmem -run='^$' -count=10 ./ | tee bench.txt
go run golang.org/x/perf/cmd/benchstat@latest bench.txt   # median ± variance
```

Read it as:
- `BenchmarkColdDialPerOp` — the handshake cost (dial + auth + one session).
- `BenchmarkWarmKayReuse` / `WarmDesops` / `WarmJolestar` — session-open cost on an
  established connection. The **warm/cold ratio** is the reuse win; the spread
  between the three warm numbers is the per-library overhead (kay's hand-rolled
  reuse vs desops's pool bookkeeping vs jolestar's borrow/return + validation).
- `BenchmarkReconnect` — the one-off recovery cost after a drop (close + redial +
  session); strategy-independent.

**Tier 2 — steady-state residency** (`membench/`):

```sh
go run ./membench -n 50 -hold 10s
```

Reports heap-in-use and goroutine deltas for holding _N_ connections open, so
per-connection cost = `(after − before)/N`. While it holds them, watch idle CPU
externally (`top -pid <printed pid>`) — it should sit near zero (only the 15 s
keepalive pings do any work).

**Tier 3 — reconnect blast radius** (manual, needs server-side control): with a
real fleet running, drop one host's connection (`ss -K dst <ip>`, restart sshd, or
an `iptables` DROP) and confirm (a) time-to-recover matches `BenchmarkReconnect`
plus the current backoff step, (b) other hosts' refresh cadence is unchanged, and
(c) the UI never blocks. kay's self-healing (backoff+jitter, probe-driven redial)
is unit-tested in `../internal/sshx/pool_internal_test.go`; the rivals have no
equivalent, so this tier is kay-only.

## Observed results (Apple M2 Pro, localhost sshd, 2026-07)

`-benchtime=200x -count=3`:

| Strategy | Time/op | Allocs/op | Notes |
|----------|--------:|----------:|-------|
| `ColdDialPerOp` (reference) | ~125 ms | 460 | full handshake per op |
| `WarmKayReuse` | ~3.7 ms | **82** | one client, session per op |
| `WarmJolestar` | ~3.7 ms | 83 | generic pool, borrow/return |
| `WarmDesops` | — | — | **deadlocked** under `MaxConnections:1` (see below) |
| `Reconnect` | ~125 ms | 460 | ≈ cold; a one-off after a drop |

Takeaways:
- **Reuse win ≈ 30×** (125 ms → 3.7 ms) and **~5.6× fewer allocations** — what the
  fleet paid per host per tick before 0.2.0, now paid once.
- **kay ≈ jolestar** on throughput (1 alloc apart): a generic pool matches kay's
  reuse, but neither jolestar nor desops has kay's reconnect/backoff/keepalive — so
  we lose nothing by not taking a dependency and gain the self-healing.
- **`desops/sshpool` deadlocked** on a tight loop with the gentle `MaxConnections:1`
  its own docs recommend. The harness now uses `MaxConnections:4, MaxSessions:10,
  SessionCloseDelay:1ms`; if it still hangs, that fragility (abandoned lib, 12★) is
  itself the finding — run the other two and note it. Always pass `-timeout` so a
  hang fails instead of wedging.

## Note on fairness

`desops/sshpool` is configured `MaxConnections: 1` (its docs recommend this to "be
gentle to your servers") so it holds one reused connection, matching kay's model.
jolestar uses its default config; borrow/return is serial so one client is reused.
All three ultimately hold an `*ssh.Client` and open a session per op — the
benchmark measures the **machinery around that**, which is the point.
