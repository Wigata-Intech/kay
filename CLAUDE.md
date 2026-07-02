# CLAUDE.md

Guidance for Claude Code (and any agent) working in this repository.

kay is a small, **single-binary Go CLI** to manage a fleet of Linux servers over
SSH: generate keys, register servers, install keys, run commands, and watch a
refreshing terminal dashboard — for one host or a whole fleet. It is part of the
**Camelot** family of tools.

Read `README.md` for the user-facing tour and `ARCHITECTURE.md` for the package
layering. Design notes live in `docs/technical-design/`.

## Core principles (non-negotiable)

- **Stdlib-first, minimal dependencies.** The ONLY third-party modules allowed
  are `golang.org/x/crypto`, `golang.org/x/sys`, and `golang.org/x/term`.
  Do **not** add a new dependency (TUI framework, CLI framework, color library,
  etc.) without the maintainer's explicit sign-off — reach for the standard
  library or the in-repo `internal/tui` toolkit instead.
- **KISS & DRY.** One SSH path (`internal/sshx`), one JSON store
  (`internal/config`), one TUI toolkit (`internal/tui`). Prefer the simplest
  change that solves the problem; don't over-engineer.
- **Minimal blast radius.** Touch only what the task needs. No drive-by rewrites.
- **Safe by default.** Public-key auth only; keys/config `0600`; host keys pinned
  TOFU with confirmation; destructive actions confirmed and target-validated;
  no telemetry. Never weaken these without explicit instruction.
- **Root causes, not band-aids.** Senior-engineer standard. If a fix feels hacky,
  stop and implement the clean version.

## Build, test & quality gate

Everything is driven by the `Makefile` (`make help` lists all targets). The gate
mirrors CI exactly — run it before claiming anything is done:

```sh
make check   # gofmt + vet + test -race + build   (fast pre-push gate)
make ci      # the above PLUS lint + gosec + vuln  (full CI mirror)
```

Individual stages: `make fmt`, `make vet`, `make test`, `make build`,
`make lint`, `make gosec`, `make vuln`, `make release-snapshot`.

Fixing a vuln finding: `make update-deps` (bumps x/crypto, x/sys, x/term and
runs `go mod tidy`).

## Verification before "done"

Do not report work complete until:

1. **Gate is green** — `make ci` passes end-to-end. Show the relevant output;
   never assert "passing" without evidence.
2. **Self-review the diff** — check it against the conventions below
   (architecture/layering, error style, security, tests). For changes to auth,
   host-key handling, key storage, or destructive actions, review with extra care.
3. **Re-gate after any fix** — loop until green AND clean.

If something goes sideways, stop and re-plan rather than pushing forward. For any
non-trivial task (3+ steps or an architectural decision), plan first and confirm
direction before implementing.

## Architecture & layering

```
cmd/kay/main.go        entrypoint + flag-based subcommands (keep THIN)
internal/config        JSON store (keys, servers)
internal/dashboard     interactive tabbed dashboard (built on internal/tui)
internal/fleet         multi-host fleet overview (kay fleet)
internal/keys          key generation + PEM I/O
internal/metrics       remote metric collection + parsing
internal/sshx          the single SSH client path (dial/run/shell, TOFU)
internal/tui           minimal stdlib TUI toolkit (screen, keys, widgets)
```

**Import rule:** the reusable library packages (`tui`, `sshx`, `metrics`) must
stay UI- and app-agnostic — they import nothing from `dashboard`, `fleet`,
`config`, or `cmd`. This keeps them extractable into a shared module later
(see `ARCHITECTURE.md`). Don't introduce upward or cyclic imports.

- Seams are interfaces: `metrics.Runner` and `dashboard.Client` both are just
  `Run(string) (string, error)`. Depend on those, not concrete SSH types.
- Metrics come from a single batched `sh -c` script over SSH (`/proc`, `df`,
  `top`, `docker ps`) — no server-side agent. Keep it one round-trip.

## Code conventions

- **Formatting:** `gofmt` is enforced by CI. Run `make fmt`.
- **Static analysis:** `make lint` runs golangci-lint v2 (staticcheck, govet,
  ineffassign, unused, misspell, unconvert, gocyclo). Keep it at **0 issues**.
  `gocyclo` fails any function whose cyclomatic complexity exceeds 15 (test files
  are excluded) — keep functions small (Go Report Card A+).
- **Error strings (staticcheck ST1005):** lowercase, no trailing punctuation or
  newline. `fmt.Errorf("no servers registered; add one with 'kay server add'")`.
- **Errors:** wrap with `%w` and context (`fmt.Errorf("dial %s: %w", host, err)`).
  Don't silently drop errors; if one is intentionally ignored, `_ = f.Close()`.
- **gosec suppressions:** the directive MUST use the hash form
  `//#nosec <RULE> -- justification` (a bare `//nosec` is NOT honored). Only
  suppress genuinely-audited cases and always include the `--` justification.
  Current suppressions: G106 (`--insecure` opt-in), G304 (paths from kay's own
  key/config store), G306 (world-readable `.pub`).
- **Comments** explain *why*, not *what*. Exported identifiers get doc comments
  (pkg.go.dev is public). No banner/restating noise — don't add a comment that
  repeats what the next line already says or that restates an obvious language
  fact (e.g. labelling a `_test` file "black-box"; the package clause says so).
- **Tests:** external **black-box** package (`foo_test`) by default so tests go
  through the exported API; use white-box (`package foo`) only when a test must
  reach unexported internals (e.g. `internal/dashboard`'s model/event loop) — and
  say why in one line. Table-driven where it fits, with consistent field names
  (`name` / inputs / `want` / `wantErr`) and `t.Run(tt.name, …)`; keep inherently
  stateful flows as ordered sequences, not forced tables. Order cases
  positive/success first, then error/edge cases in code-flow order. `go test
  -race` must pass; add coverage when you touch parsing, the store, or key
  handling.

## Git & PR conventions

- `main` is **branch-protected** — never commit directly to it. Work on a
  branch and open a PR.
- Branches: `type/short-slug` (e.g. `chore/ci-quality-gates`, `fix/net-align`).
- **Conventional Commits** (`feat:`, `fix:`, `chore:`, `docs:`, `ci:`, `refactor:`).
- CI gates every PR: `build-test` (ubuntu+macos), `lint`, `gosec`, `govulncheck`,
  and `CodeQL` (advanced setup). A PR isn't ready until all are green.
- **CHANGELOG discipline:** add an entry under `## [Unreleased]` (Keep a Changelog
  sections: Added/Changed/Fixed/Security) for any user-facing change. The GitHub
  Release notes are extracted from the tagged version's `CHANGELOG.md` section
  (`release.yml` → GoReleaser `--release-notes`), **not** from commit messages — so
  keep the changelog curated and human-readable.
- Releases: finalize `[Unreleased]` into `## [X.Y.Z] - <date>` with compare links,
  then push a `vX.Y.Z` tag → the release workflow runs GoReleaser. See
  `RELEASING.md`.
- Dependabot manages weekly gomod + github-actions bumps; those PRs are generally
  safe to merge once CI is green.

## Where things live

- `README.md` — user docs · `ARCHITECTURE.md` — layering · `SECURITY.md` — model
- `CONTRIBUTING.md` / `CODE_OF_CONDUCT.md` — contributor rules
- `assets/` — demo pipeline (`demo.tape` / `blur.sh` → `demo.gif`); technical
  design docs live in the Camelot vault (`../../docs/technical-design/`)
- `.github/workflows/` — `ci.yml` (gates) · `codeql.yml` (code scanning) · `release.yml` (GoReleaser)
- `Makefile` — all dev commands · `.goreleaser.yaml` — release build

## Personal context

If `CLAUDE.local.md` exists, read it — it holds per-user, machine-specific notes
(local paths, test-host aliases, personal preferences). It is **gitignored and
must never be committed or pushed.**
