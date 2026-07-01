# Contributing to kay

Thanks for your interest in improving `kay`. This is a small, focused tool —
contributions that keep it lean and dependency-light are very welcome.

## Ground rules

- **Dependencies:** standard library plus `golang.org/x/crypto` and
  `golang.org/x/term` only. New third-party dependencies need a strong
  justification and a discussion issue first.
- **Style:** run `gofmt` (or `goimports`) and `go vet ./...` before committing.
- **Tests:** add or update tests for behaviour you change. Pure logic (parsers,
  key handling, rendering, input decoding) must be unit-tested; the renderer is
  a pure function so size cases are testable without a terminal.
- **Scope:** keep changes aligned with the project's goals (see the README).
  Larger ideas: open an issue to discuss before a big PR.

## Development

```sh
go mod tidy
go vet ./...
go test ./...
go build ./cmd/kay
```

To exercise the SSH paths without a remote host, run a local `sshd` and connect
to `127.0.0.1` (see the "Verifying locally" section of the README).

## Pull requests

1. Fork and create a feature branch (`type/short-slug`).
2. Use **Conventional Commits** (`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`,
   `ci:`, `test:`). Keep commits focused.
3. Add a **`CHANGELOG.md`** entry under `## [Unreleased]` for any user-facing
   change (Added / Changed / Fixed / Security). Release notes are generated from
   the CHANGELOG section at tag time — **not** from commit messages — so write it
   for humans. Internal-only churn (pure refactors/tests) can be summarized in one
   line or omitted.
4. Ensure `make ci` is clean (gofmt, vet, `test -race`, lint 0 issues, gosec,
   govulncheck) — at minimum `go vet`, `go test`, and `gofmt -l`.
5. Describe the change and how you tested it. Reference any related issue.

## Reporting bugs / requesting features

Open a GitHub issue with steps to reproduce (for bugs) or a clear use case
(for features). For security issues, follow [SECURITY.md](SECURITY.md) instead.

By contributing, you agree that your contributions are licensed under the
project's [Apache-2.0 license](LICENSE).
