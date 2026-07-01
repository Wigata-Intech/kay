# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-07-01

Initial release.

### Added

- **Key management** — generate ed25519/RSA keys (passphrase-protected
  supported), list them, and show the public key.
- **Server registry** — stored as JSON; select servers by alias or from an
  interactive list.
- **Key install** — `install` prints the exact `authorized_keys` command;
  `install --push` appends the key over a one-time password login.
- **`connect`** (interactive shell) and **`exec`** (one-shot command).
- **`dashboard`** — a full-screen, tabbed, colour TUI (Overview / Processes /
  Docker / Network / Disk) with a moving cursor, selectable rows, and guarded
  actions — kill (SIGTERM/SIGKILL) and Docker restart/stop/logs/inspect — each
  behind a `y/N` confirmation. Includes:
  - windowed layout (header bar + titled framed pane), colour threshold gauges,
    CPU/memory sparklines, and a two-column Overview on wide terminals;
  - Docker health colouring + counts, active-interface highlighting,
    instantaneous per-process CPU via `top`, process sort cycling (`s`), and a
    Disk tab covering all filesystems;
  - a scrollable, searchable logs/inspect pager — `/` search with match
    highlighting and a current-match marker, `n`/`N`, and horizontal scroll for
    long lines;
  - vim-friendly navigation (`j/k`, `g/G`, `Ctrl-U/Ctrl-D`, `[`/`]`), with
    metrics collected off the event loop so input stays responsive;
  - `--read-only` mode that disables all destructive actions;
  - `--anonymize` (or `KAY_DEMO=1`) to mask host/user/alias + Docker names for
    demos and screenshots.
- **`fleet`** — a live, one-row-per-host overview of all registered servers,
  collecting metrics concurrently.
- **SSH keep-alive** with automatic reconnect after a dropped connection.
- **Host-key verification** — trust-on-first-use pinned to `known_hosts` with an
  explicit confirmation prompt; `--insecure` bypasses for lab hosts.
- **`internal/tui`** — a small, dependency-free terminal toolkit (alt-screen
  lifecycle, key decoding, box / tab-bar / list widgets).
- **Cross-platform client** (macOS / Linux / Windows; Windows console VT/ANSI
  auto-enabled). Target servers are Linux.
- **`version`** command reporting build version / commit / date.

### Security

- Private keys and config stored with `0600` permissions.
- Destructive actions require explicit confirmation; process PIDs and Docker
  IDs/names are validated before use.
- Public-key authentication only (password used solely for assisted install);
  no telemetry.

[Unreleased]: https://github.com/Wigata-Intech/kay/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Wigata-Intech/kay/releases/tag/v0.1.0
