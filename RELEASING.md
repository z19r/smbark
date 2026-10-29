# Releasing smbark

This file describes how an smbark release is cut and what each moving part
does.

## Short answer

The supported release path is:

```text
just release patch|minor|major
```

That command runs the release gate, bumps `VERSION`, generates the
changelog section from conventional commits, pushes a `release/vX.Y.Z`
branch, opens a release PR, waits for CI, squash-merges it, and watches
the release workflow.

The merge changes `VERSION` on `main`, which starts
`.github/workflows/release.yml`. It re-runs the release gate, creates the
`vX.Y.Z` tag, runs goreleaser to build the binaries and publish the GitHub
release, and (when enabled) publishes to the AUR.

## The pieces

| File | Role |
|------|------|
| `VERSION` | Single source of truth for the version (no `v` prefix), embedded into the binary by `version.go`. |
| `CHANGELOG.md` | Keep-a-Changelog history; `## [Unreleased]` at the top. |
| `SECURITY.md` | Names the current supported release; bumped by `just release`. |
| `justfile` | `release-check`, `release-dry-run`, `release`, `prerelease`. |
| `.goreleaser.yml` | Build matrix, raw binaries, checksums, GitHub release. |
| `.github/workflows/ci.yml` | Format + build + vet + test on push/PR. |
| `.github/workflows/release.yml` | Tag + goreleaser + AUR when `VERSION` changes on `main` (or a `v*` tag is pushed). |
| `aur/smbark`, `aur/smbark-bin` | AUR PKGBUILDs; `pkgver` is stamped from the tag at publish time. |

## Commit messages drive the changelog

Release notes are generated from the subjects of non-merge commits since
the previous tag (PR titles, since PRs are squash-merged):

| Subject prefix | Section |
|----------------|---------|
| `feat:` / `feat(scope):` | Added |
| `fix:` / `fix(scope):` | Fixed |
| `refactor` `perf` `chore` `ci` `docs` `test` `build` `style` | Changed |

Anything else (no conventional prefix) is left out. If nothing matches,
the section reads "version bump only". Entries already under
`## [Unreleased]` are kept and land under the new version too.

## Local flow: `just release <level>`

`just release-dry-run <level>` runs the same gate without changing
anything.

Preconditions:

- clean working tree
- on `main` (offers to switch if not)
- `main` fast-forwards to `origin/main`

Steps:

1. `just release-check`: gofmt/golines, `go vet`, `go build`, `go test`.
2. Pull `main`, fetch tags, and compute the next version from `VERSION`.
3. Write `VERSION` and bump the supported release in `SECURITY.md`.
4. Replace `## [Unreleased]` with a fresh empty one above
   `## [X.Y.Z] - <today>` and the generated sections; update the link
   footer.
5. Commit `release: vX.Y.Z` on `release/vX.Y.Z`, push, open a PR.
6. Wait for CI (`gh pr checks --watch --fail-fast`), then squash-merge
   unless it was already auto-merged.
7. Return to `main`, pull, and `gh run watch` the release workflow.

If CI fails the recipe stops with the PR open; fix it on the branch and
merge, and the release workflow runs on merge as usual.

## Prereleases: `just prerelease`

Cuts `vX.Y.Z-beta.N` from the current `VERSION` (which is left alone),
promoting a non-empty `## [Unreleased]` into a dated beta section. It
commits to `main` and pushes the tag directly; the tag push starts the
release workflow, and goreleaser marks the GitHub release as a
prerelease. Betas are never published to the AUR.

## Publishing (CI)

`.github/workflows/release.yml`:

1. reads the tag from `VERSION` (or the pushed tag)
2. re-runs `just release-check`
3. creates and pushes `vX.Y.Z` if it does not exist yet
4. runs goreleaser (`release --clean`), which builds `smbark` for linux
   on amd64 and arm64, publishes them as raw binaries named
   `smbark-linux-<arch>` (the names `site/install.sh`, the site's download
   links and `aur/smbark-bin` depend on), generates `checksums.txt`, and
   creates the GitHub release
5. attests build provenance over `dist/checksums.txt`
6. if the `PUBLISH_TO_AUR` repo variable is `true`, publishes
   `smbark-bin` and `smbark` to the AUR (needs the `AUR_SSH_KEY` secret)

Re-running a failed release workflow is safe: an existing tag is reused.

## Contributor checklist

1. use conventional PR titles (`feat:`, `fix:`, `chore:` …)
2. `just release-dry-run patch|minor|major`
3. `just release patch|minor|major`
4. review the generated notes in the release PR

## Verifying goreleaser config locally

If goreleaser is installed:

```text
goreleaser check
goreleaser release --snapshot --clean   # dry run, no publish
```

## Verifying release provenance

The release workflow signs a SLSA build-provenance attestation over
`dist/checksums.txt`, which covers every published binary. Anyone can
verify a downloaded artifact:

```text
gh attestation verify smbark-linux-amd64 --repo z19r/smbark
```

Attestations only exist for tags released after this workflow step landed.
