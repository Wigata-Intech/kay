# Releasing kay

Maintainer guide for cutting a release. Releases are tag-driven: pushing a
`vX.Y.Z` tag to `main` triggers the release workflow
([`.github/workflows/release.yml`](.github/workflows/release.yml)), which runs
GoReleaser ([`.goreleaser.yaml`](.goreleaser.yaml)) to build the cross-platform
binaries and publish a GitHub Release.

## Versioning (SemVer)

kay follows [Semantic Versioning](https://semver.org/):

- **patch** (`0.1.0 → 0.1.1`) — quality, internal, tooling, or docs work; no new
  features and no behavior change.
- **minor** (`0.1.0 → 0.2.0`) — new, backward-compatible features.
- **major** (`0.1.0 → 1.0.0`) — breaking changes to the CLI, flags, or on-disk
  store format.

## Steps

1. **Green `main`.** Ensure CI is green and `make ci` passes locally
   (tests, lint 0 issues, gosec `Issues: 0`, govulncheck clean).

2. **Finalize the changelog.** In [`CHANGELOG.md`](CHANGELOG.md), move the
   `[Unreleased]` entries into a new dated section and refresh the compare links
   at the bottom:

   ```markdown
   ## [Unreleased]

   ## [X.Y.Z] - YYYY-MM-DD
   ...entries...
   ```

   ```markdown
   [Unreleased]: https://github.com/Wigata-Intech/kay/compare/vX.Y.Z...HEAD
   [X.Y.Z]: https://github.com/Wigata-Intech/kay/compare/vPREV...vX.Y.Z
   ```

   Commit this on `main` (via PR) **before** tagging.

3. **Tag and push.** From an up-to-date `main`:

   ```sh
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

   The `release` workflow extracts this version's `CHANGELOG.md` section as the
   GitHub Release notes (`--release-notes`), so the changelog **is** the release
   body — there is no separate notes step. A missing section fails the release.

4. **Watch the release run.** The `release` workflow runs GoReleaser and creates
   the GitHub Release with built artifacts. Confirm it succeeds:

   ```sh
   gh run watch --repo Wigata-Intech/kay
   gh release view vX.Y.Z --repo Wigata-Intech/kay
   ```

## Verify after release

- **pkg.go.dev** — [pkg.go.dev/github.com/Wigata-Intech/kay](https://pkg.go.dev/github.com/Wigata-Intech/kay)
  shows the new version with **License: Apache-2.0** and "Redistributable
  license" (pkg.go.dev re-detects the LICENSE only on a new tagged release).
- **README badges** — the `release` badge resolves to the published release; CI
  and Go Reference badges are green.
- **Install** — `go install github.com/Wigata-Intech/kay/cmd/kay@vX.Y.Z` works.

## Dry run

Build the release locally without publishing:

```sh
make release-snapshot   # GoReleaser --snapshot --clean; writes to dist/ (gitignored)
```
