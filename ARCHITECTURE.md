# Architecture

`kay` is a single Go module (`module github.com/Wigata-Intech/kay`) with a
deliberately layered package structure. The point of the layering is twofold:
keep the code easy to reason about, and keep the reusable pieces **cleanly
separable** so they can later be lifted into a separate shared module with
minimal churn — without doing that move now.

## Layout

Alphabetical (matching how editors and the GitHub UI sort):

```
cmd/kay            entrypoint + subcommand dispatch (the application)
internal/
├── config         persistent JSON store (keys, servers)         [app]
├── dashboard      interactive tabbed single-host dashboard      [app]
├── fleet          multi-host fleet overview (kay fleet)         [app]
├── keys           key generation + PEM I/O                      [app]
├── metrics        remote metric collection + parsing            [library]
└── tui            minimal terminal UI toolkit                   [library]

The SSH client path and self-healing connection pool live in the external
module github.com/Wigata-Intech/w-tools/x/sshx (extracted from this repo).
```

## Dependency layering

Arrows point "depends on". There are no cycles.

```
cmd/kay ─┬─▶ config
         ├─▶ dashboard ─┬─▶ config
         │              ├─▶ metrics
         │              └─▶ tui
         ├─▶ fleet ─┬─▶ config
         │          ├─▶ metrics
         │          ├─▶ w-tools/x/sshx
         │          └─▶ tui
         ├─▶ keys ──▶ config, w-tools/x/sshx/keys
         └─▶ w-tools/x/sshx
```

| Package | Class | Imports (intra-project) | Promotable? |
|---------|-------|--------------------------|-------------|
| `config` | app | none | ➖ standalone but app-specific |
| `dashboard` | app | `config`, `metrics`, `tui` | ✕ application UI |
| `fleet` | app | `config`, `metrics`, `w-tools/x/sshx`, `tui` | ✕ application UI |
| `keys` | app | `config` | ➖ app-specific |
| `metrics` | library | none | ✅ yes |
| `tui` | library | none | ✅ yes |
| `cmd/kay` | app | all | ✕ binary |

**Key property:** the *library* packages (`metrics`, `tui`) import nothing
from `kay` — the same discipline that let `sshx` graduate into the shared
`w-tools/x/sshx` module. Easy to verify:

```sh
grep -rhoE '"github.com/Wigata-Intech/kay/[^"]+"' internal/metrics internal/tui
# (should print nothing)
```

## Why a future extraction is easy

Today these packages live under `internal/`, so other modules can't import them
— intentional while the API is unstable. If we later publish the reusable
pieces as their own shared module, extraction is close to a copy:

1. Move each library dir out of `internal/` into the new module.
2. Update its import path (e.g. `…/kay/internal/tui` → `…/<shared-module>/tui`).
3. In `kay`, depend on the shared module and update imports; the `app` packages
   are untouched in shape because they already consume these only via small
   interfaces (`metrics.Runner`, `dashboard.Client`).

Because the library packages are dependency-free and the app talks to them
through narrow interfaces, nothing in `config`/`keys`/`dashboard`/`fleet` needs
redesigning to make the move.

## Rules to preserve this

- Library packages (`metrics`, `tui`) must **not** import `config`,
  `keys`, `dashboard`, `fleet`, or `cmd`.
- Prefer small interfaces at the boundary (e.g. `metrics.Runner`,
  `dashboard.Client`) over concrete cross-package types.
- Keep `cmd/kay` thin: argument parsing and wiring only.
