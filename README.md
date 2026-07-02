# kay

> A steward's watch over your realm of servers.

[![CI](https://github.com/Wigata-Intech/kay/actions/workflows/ci.yml/badge.svg)](https://github.com/Wigata-Intech/kay/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Wigata-Intech/kay)](https://github.com/Wigata-Intech/kay/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/Wigata-Intech/kay.svg)](https://pkg.go.dev/github.com/Wigata-Intech/kay)
[![Go Report Card](https://goreportcard.com/badge/github.com/Wigata-Intech/kay)](https://goreportcard.com/report/github.com/Wigata-Intech/kay)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/Wigata-Intech/kay/badges/coverage.json)](https://github.com/Wigata-Intech/kay/actions/workflows/coverage.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

A small, single-binary CLI to manage a fleet of Linux servers over SSH:
generate keys, register servers, install keys, run commands, and watch a
refreshing metrics dashboard — for one host or your whole fleet.

![kay dashboard demo](assets/demo.gif)

Built with the Go standard library plus the `golang.org/x` `crypto`, `term`, and
`sys` packages (the only third-party dependencies). Design: KISS, DRY —
one SSH path, one JSON store, a small in-repo TUI toolkit instead of a framework.

Part of the **Camelot** tools.

## TL;DR

- **One binary, no agent.** Everything runs from your machine over plain SSH;
  nothing is installed on the servers.
- **Full loop in four commands:** generate a key → register a server → install
  the key → open a live dashboard.
- **Live terminal dashboard.** Tabbed, colour, refreshing: CPU, memory, disk,
  load, per-interface network I/O, top processes, and Docker containers — with a
  cursor and guarded actions (kill / restart / stop / logs / inspect).
- **Fleet view.** `kay fleet` shows one live row per registered host.
- **Safe by default.** Public-key auth only, host keys pinned on first use,
  destructive actions confirmed, keys and config stored `0600`, no telemetry.
- **Tiny and auditable.** Stdlib + `x/crypto`, `x/term`, `x/sys`, KISS/DRY throughout.

## Quick Setup

Install the latest release:

```sh
go install github.com/Wigata-Intech/kay/cmd/kay@latest
```

Then generate a key, register a host, and watch it live:

```sh
kay key gen --name default                                       # ed25519 (default)
kay server add --alias prod-1 --host 203.0.113.10 --user ubuntu --key default
kay install --alias prod-1        # prints the authorized_keys command (add --push to bootstrap now)
kay dashboard --alias prod-1 --interval 2s
```

Day-to-day:

```sh
kay connect --alias prod-1                 # interactive shell
kay exec --alias prod-1 -- uptime          # one-shot command
kay exec --alias prod-1 -- docker ps
kay fleet --interval 5s                    # all registered hosts, one row each
kay ls                                     # everything you've registered
```

Omitting `--alias` on `connect`, `exec`, or `dashboard` lets you pick a server
from a numbered list. `kay help` lists every command. Want to try it without a
remote box? See [Verifying locally](#verifying-locally-with-your-own-sshd).

## Prerequisite

- Go 1.26+ (declared in `go.mod`) to build from source.
- Remotes: Linux with `sshd` and standard tools (`/proc`, `ps`, `nproc`).
  `docker` is optional — the dashboard shows it only if present.

## Build

The first build needs network access to fetch `x/crypto` (and `x/sys`,
`x/term`):

```sh
go mod tidy        # resolves and pins x/crypto, x/sys, x/term; writes go.sum
go build -o kay ./cmd/kay
```

Or install the latest release directly:

```sh
go install github.com/Wigata-Intech/kay/cmd/kay@latest
```

Offline builds: run `go mod vendor` once (with network), then
`go build -mod=vendor ./cmd/kay`.

## Test

```sh
make check     # gofmt, go vet, go test -race, build (the CI build-test gate)
make ci        # the above plus lint, gosec, and govulncheck
```

Or run the underlying tools directly:

```sh
go vet ./...
go test ./...
```

Unit tests cover the config store round-trip, key generation + signer loading
(ed25519 and RSA), and the metrics parser against a fixture. See the `Makefile`
(`make help`) for the full list of local checks, which mirror CI exactly.

## Dashboard, Fleet & Verifying

### Dashboard (interactive TUI)

The dashboard is a full-screen, tabbed terminal UI with colour gauges, a moving
cursor, and guarded actions. It runs in the terminal's alternate screen, so it
never pollutes your scrollback and restores your previous view on exit.

```
Tabs    : Tab / Shift-Tab · [ / ] · H / L · or 1-5   → Overview · Processes · Docker · Network · Disk
Global  : r refresh now · +/- change interval · ? help (full keymap) · q quit
List    : ↑↓ or j/k select · PgUp/PgDn or ^U/^D page · g/G top/bottom · Enter details/inspect
Process : s cycle sort (CPU/MEM/PID/name) · x SIGTERM · X SIGKILL   (asks y/N first)
Docker  : l logs · t stats (top by CPU/MEM) · R restart · x stop   (restart/stop ask y/N first)
Stats   : c sort CPU · m sort MEM · r reload · j/k select · Esc back
Disk    : Enter/l explore a mount (du) — dirs & files by size; . toggles hidden;
          j/k select · Enter/l/→ open dir · h/←/⌫ up · Esc back  (opening a file: notice)
Detail  : j/k ↑↓ scroll · h/l ←→ pan · g/G ends · / search (n/N next) · Esc/q back
Overview: o customise panels — j/k select · J/K move · space hide · w save · Esc cancel
```

Keys are vim-friendly throughout: `h/j/k/l` move, `g/G` jump to ends, `H/L`
switch tabs, and `Esc`/`q` back out of any overlay.

The Overview is a **responsive grid** of panels — System (CPU + per-core + load),
Memory (RAM + swap + cached), Top processes, Disk (space + inodes), Network,
Docker, Connections, and Services (failed units) — flowing into one, two, or
three columns as the terminal widens, with CPU/memory sparkline history. Press
**`o`** to customise it: reorder panels with `J`/`K` and hide them with `space`,
then `w` to save. Your layout persists in `config.json` and applies to every
host's dashboard.

Navigation is vim-friendly (`j/k`, `g/G`, `h/l`, `Ctrl-U/Ctrl-D`). The
inspect/logs overlay is a scrollable, horizontally-pannable pager with `/`
search that highlights matches and marks the current one. Pass `--read-only`
to disable all destructive actions (kill / restart / stop). Docker status is
colour-coded by health and active network interfaces are highlighted.

Colour is automatic (respects `NO_COLOR` and `TERM=dumb`); force it with
`--color always|never`. Thresholds: green < 70 %, amber 70–89 %, red ≥ 90 %.
On a terminal smaller than 40×10 it shows a "too small" hint until enlarged; it
reflows on resize. Piping the output (not a TTY) prints plain timestamped
snapshots instead.

For demos and screenshots, `--anonymize` (or `KAY_DEMO=1`) masks the host, user,
alias, and Docker names so nothing confidential (IPs, hostnames, service names)
appears on screen.

State lives in `<user-config-dir>/kay/` (`config.json`, `known_hosts`, and a
`keys/` directory of PEM files). Set `KAY_HOME` to override.

### Fleet

`kay fleet` connects to every registered server concurrently and renders one live
row per host — alias, reachability, CPU, memory, load, and Docker container counts
— so you can scan the whole realm at a glance. Press **Enter** on a host to drill
straight into its full dashboard, and **Esc**/**q** to return to the overview —
the terminal is handed over seamlessly (one screen, one input reader, no flicker),
and **Ctrl-C** exits the whole app. It shares the same refresh controls as the
dashboard (`r`, `+/-`, `q`) and honours `--anonymize`.

#### Persistent, self-healing connections

The fleet keeps **one long-lived SSH connection per host** and reuses it for every
refresh, rather than reconnecting each tick. This matters at scale:

- **Cheap refreshes.** An SSH connection multiplexes many sessions. After the
  first connect pays the key-exchange + public-key-auth handshake, each refresh is
  just a new session over the existing transport — no re-handshake, negligible CPU
  and network. The single-host `kay dashboard` has always worked this way; the
  fleet now does too.
- **Kinder to your servers.** Reconnecting every few seconds runs a full
  auth/PAM cycle per connection, floods each host's `auth.log`, and — for a whole
  fleet reconnecting in lockstep — pushes against sshd's `MaxStartups` throttle
  (which starts *dropping* connections past its limit). Persistent connections
  eliminate that churn.
- **Bounded cold start.** Concurrent connects are capped (16 at a time) so
  bringing up a large fleet can't self-throttle or exhaust local sockets.
- **Self-healing.** A dropped connection is detected (by a periodic keepalive
  probe or a failed refresh) and re-established automatically, with exponential
  backoff + jitter so many hosts recovering from one blip don't stampede. A host
  that is still connecting or offline shows a brief message on **Enter** instead of
  drilling in; a ready host opens instantly.
- **Zero-handshake drill-in.** Pressing **Enter** hands the dashboard the exact
  connection the fleet already holds — no second handshake — and that dashboard
  inherits the same self-healing.

Design notes and the benchmark/load/stress methodology live in the Camelot
technical-design vault (`docs/technical-design/[4]ssh-connection-pool.md`).

### Verifying locally with your own sshd

You can exercise the full flow against a local SSH server without a remote box:

```sh
KAY_HOME=$(mktemp -d)
./kay key gen --name local
# authorise it for your own account:
mkdir -p ~/.ssh && ./kay key show --name local >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys
./kay server add --alias localhost --host 127.0.0.1 --user "$USER" --key local
./kay exec --alias localhost -- 'uname -a'
./kay dashboard --alias localhost --interval 1s
```

## Project Structure

```
cmd/kay/main.go            entrypoint + subcommands
internal/config            JSON store (keys, servers)
internal/dashboard         interactive tabbed dashboard built on internal/tui
internal/fleet             multi-host fleet overview (kay fleet)
internal/keys              key generation + PEM I/O
internal/metrics           remote metric collection + parsing
internal/sshx              the single SSH client path (dial/run/shell, TOFU)
internal/tui               minimal stdlib TUI toolkit (screen, keys, widgets)
```

`internal/tui` is a small, dependency-free terminal toolkit (alternate-screen +
raw-mode lifecycle, keyboard decoding, a tab bar, and a scrollable selectable
list). It exists so the SSH/metrics layers stay UI-agnostic; if you ever want a
richer UI, it can be swapped for a library like `tview` or Bubble Tea with
changes confined to the `dashboard` package.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the package layering and how the
reusable packages (`tui`, `sshx`, `metrics`) are structured for easy future
extraction into a shared module.

## Goals, Capabilities & Scope

### Goals

- Make it trivial for a single operator to generate a key, authorise it on a
  server, connect, run commands, and watch a host's vitals — from one CLI.
- Stay tiny and auditable: minimal dependencies, readable code, no agent on the
  remote.
- Be safe by default: explicit host-key trust, confirmations for destructive
  actions, restrictive file permissions.

### Capabilities

- Generate and store ed25519 / RSA keys (passphrase-protected supported).
- Register servers (alias, host, port, user, key) in a local JSON store and
  select them by name or interactively.
- Print the exact `authorized_keys` install command for a server.
- Interactive shell (`connect`) and one-shot commands (`exec`).
- Tabbed, colour, refreshing dashboard: CPU, memory, disk, load, per-interface
  network I/O, top processes (instantaneous CPU), and Docker containers — with a
  cursor, selectable rows, and guarded actions (kill / docker restart, stop,
  logs, inspect).

### Out of scope (for now)

- No server-side agent or daemon; metrics come from standard commands over SSH.
- No password authentication, SSH certificates, or jump/bastion hosts.
- No multi-user or team features; state is per-operator and local.
- Not a replacement for full monitoring stacks (Prometheus/Grafana) — it's an
  at-a-glance operator tool.

The **client** runs on macOS, Linux, and Windows (the dashboard is best on
macOS/Linux and Windows Terminal; legacy Windows consoles need VT/ANSI, a
pending polish item). **Target servers are Linux/Ubuntu** with `sshd` and
standard tools.

### Roadmap

| Item | Status | Notes |
|------|--------|-------|
| Key management, server registry, install, connect, exec | ✅ Done | Core CLI |
| Interactive tabbed dashboard (Overview / Processes / Docker / Network) | ✅ Done | Colour, cursor, guarded actions |
| Windowed framed-pane layout | ✅ Done | Header bar + titled pane |
| Vim navigation + scrollable, searchable detail/logs | ✅ Done | `j/k`, `g/G`, `^U/^D`, `/` search |
| Passphrase keys · host-key consent · Unix build tags | ✅ Done | Security / portability |
| Open-source scaffolding (LICENSE, CI, SECURITY, …) | ✅ Done | |
| Search-highlight + horizontal scroll in logs/inspect pager | ✅ Done | `/` highlights matches, `h/l` pans |
| `--read-only` mode (disable destructive actions) | ✅ Done | For shared/audited sessions |
| SSH keep-alive + automatic reconnect | ✅ Done | Survives dropped connections |
| Container health colouring + active-interface highlight | ✅ Done | Green/red status, active ifaces |
| Two-column (multi-pane) Overview | ✅ Done | Gauges \| top processes on wide terminals |
| Cross-platform clients (macOS · Linux · Windows) | ✅ Done | Windows console VT/ANSI auto-enabled; CLI works everywhere |
| Process sort cycling (`s`) | ✅ Done | CPU / MEM / PID / name |
| Disk tab (all filesystems) | ✅ Done | Per-mount usage bars |
| CPU/memory history sparklines | ✅ Done | On the Overview |
| Assisted key install over an existing connection | ✅ Done | `install --push` (password bootstrap) |
| Per-pane titles on two-column Overview | ✅ Done | System \| Top processes |
| Multi-server fleet overview (one row per host) | ✅ Done | `kay fleet` — concurrent multi-host live table |
| Persistent, self-healing fleet SSH connections | ✅ Done | v0.2 — one long-lived connection per host (`sshx.Pool`/`Managed`); reuse, backoff+jitter, dial cap, zero-handshake drill-in |
| Customisable Overview (reorder / hide panels) | ✅ Done | v0.2 — `o` in the Overview tab; layout persists in `config.json` (`ui` section, schema `version`) |
| Richer Overview (docker health counts, sparklines) | ✅ Done | More than gauges |
| Demo/anonymize mode (`--anonymize` / `KAY_DEMO`) | ✅ Done | Masks host/user/alias/Docker names for screenshots |
| CI quality gates (lint · gosec · govulncheck) | ✅ Done | golangci-lint 0 issues + gosec + govulncheck in CI and `make ci` |
| Tech debt: reduce cyclomatic complexity (gocyclo) | ✅ Done | Every function ≤15; `gocyclo` gate enforces it — Go Report Card A+ |
| Tech debt: shared UI helpers (dedupe dashboard/fleet) | ✅ Done | v0.1.2 — `tui.SetColorMode`/`ClampAll`/`FirstLine`/`ThreshColor` |
| Tech debt: split large files (`dashboard.go`, `main.go`) | ✅ Done | v0.1.2 — `dashboard.go` → input/render/format |
| Tech debt: broaden tests (fleet, actions, sshx) | ✅ Done | v0.1.2 — testability seams; coverage 66.9% → 73.7% |
| Tech debt: interface/type cleanups (`Runner`/`Client`, `List`/pager) | ✅ Done | v0.1.2 — `Client`=`metrics.Runner`; `List`/`Pager` split |
| Disk explorer (`du` drill-down of what's using space) | ✅ Done | v0.2 — Enter a mount in the Disk tab to walk directories by size |
| Fleet drill-in (open a host's dashboard from fleet) | ✅ Done | v0.2 — Enter a host in `kay fleet`; seamless shared-terminal handoff |
| Customizable Overview (pinned panels) | 🚧 v0.2 | Layout config in the store |
| Top-N containers by CPU/MEM (`docker stats`) | ✅ Done | v0.2 — `t` in the Docker tab; on-demand overlay, sort by CPU/MEM |
| Agentic DevOps/SRE integration | 💡 Idea | Expose metrics + guarded actions as a structured tool/API so an AI agent can observe and remediate — deploy, restart/roll back, set/rotate env vars, run runbooks — gated by confirmations, `--read-only`, and an audit log |

## Security

### Security model

See [SECURITY.md](SECURITY.md). In brief: public-key auth only; keys and config
stored `0600`; host keys pinned with confirmation on first use (`--insecure`
bypasses, for lab use only); destructive actions need explicit confirmation and
validated targets; no telemetry.

### Security notes

- Private keys are written `0600`; the config is `0600`.
- Host keys are pinned trust-on-first-use into `known_hosts`. A later mismatch
  is a hard error (possible MITM). `--insecure` disables verification — use
  only for throwaway/lab hosts.
- Only public-key auth is supported; password auth is intentionally out of
  scope.

## The name

kay is named for **Sir Kay** — King Arthur's foster-brother and the
**seneschal of Camelot**: the steward who ran the court's household, supplies,
and logistics so the king and his knights could do their work. That's what this
tool does for your servers — it keeps watch over the fleet, keeps things in
order, and hands you the controls when you need them.

kay is the first of the **Camelot** tools: small, focused, single-binary
utilities named for the legend, each doing one job well. (Fittingly, the
all-seeing counselor **Merlin** is reserved for what comes next — an agent that
can act on your behalf.)
