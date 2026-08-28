# Releasing

task-manager has two release artifacts: the **`taskmgr` CLI binary** and the
**`sdk/tasks` Go module**. The CLI binary is built and published automatically by
**[GoReleaser](https://goreleaser.com)** running in GitHub Actions; the SDK module
is released by pushing a module-path tag (Go consumers fetch it straight from the
tag — there is nothing to build).

## Versioning

- Semantic version tags: `vX.Y.Z`.
- `taskmgr version` prints the build metadata stamped via `-ldflags` into
  `cmd.Version` / `Commit` / `Date`. GoReleaser stamps these from the tag (see
  `.goreleaser.yaml`); a local `make build` / `mise run build` stamps them from
  `git describe --always`, so an untagged checkout reports the short SHA — `dev`
  only when git is unavailable.
- With no `-ldflags` at all (`go install …/cmd/taskmgr@vX.Y.Z`), `cmd/root.go`
  falls back to `debug.ReadBuildInfo` for the module version and `vcs.revision`.
- The SDK is a **separate Go module** (`sdk/`). Go consumers pin it with a
  module-path tag, `sdk/vX.Y.Z` (`go get …/sdk@vX.Y.Z`). Keep it in step with the
  CLI tag.
- The CLI module **depends on the SDK by version** (`require …/sdk vX.Y.Z`, no
  `replace`). Local development uses the committed `go.work` workspace, so the CLI
  builds against the in-tree SDK; released builds and downstream `go install`
  ignore `go.work` and resolve the SDK from its published `sdk/vX.Y.Z` tag. **That
  is why the SDK tag must be pushed before the CLI tag** (see step 4).

## Cutting a release

Pushing a `vX.Y.Z` tag triggers `.github/workflows/release.yml`, which runs
GoReleaser to cross-compile the binary, build per-platform archives + a
`checksums.txt`, and open a **draft** GitHub release. Run from the repository root
on a clean, up-to-date tree.

1. Confirm a clean tree and sync:

   ```bash
   git status
   git pull --rebase
   ```

2. Green gate — the full pre-handoff gate, not the fast one
   (see [TESTING.md](TESTING.md)):

   ```bash
   mise run quality:full
   ```

3. Pick the version and confirm it is unused:

   ```bash
   git tag --list "v*"
   ```

4. **Release the SDK module first** (the CLI depends on it by version). Tag and
   push the SDK on the release commit, then pin the CLI to the just-published tag.
   `GOWORK=off` bypasses the dev workspace so the real published module is
   resolved into `go.mod` + `go.sum`:

   ```bash
   git tag -a sdk/vX.Y.Z -m "sdk/tasks vX.Y.Z"
   git push origin sdk/vX.Y.Z      # does NOT trigger the Release workflow

   GOWORK=off go get github.com/hk9890/task-manager/sdk@vX.Y.Z
   GOWORK=off go mod tidy
   ```

   The pin is an ordinary code change: land it via PR, never directly on `main`
   ([CHANGE-WORKFLOW.md](CHANGE-WORKFLOW.md)). Both tags are cut on the merged
   `main` commit.

   Then confirm the pin actually resolves:

   ```bash
   mise run verify:pin      # GOWORK=off go build ./...
   ```

   **This is the check to run by hand — nothing else stops a stale pin before the
   tag.** `mise run quality:full` and every CI job build inside the committed
   `go.work`, which wires the CLI to the in-tree SDK, so a `go.mod` still pinning
   the *previous* `sdk` version passes all of them while
   `go install …/cmd/taskmgr@vX.Y.Z` fails to compile for every user.

   The release workflow runs the same build, but it is **fatal only on a tag
   push** — where the tag is already public by the time it fires. A dry run and
   the weekly schedule log a warning and continue: between releases the CLI
   legitimately uses SDK symbols that are not published yet, so the build is
   expected to fail on an arbitrary `main`, and failing the job on that would
   make the dry run unrunnable outside a release sequence — which is the window
   where a signing change can be rehearsed with no version at stake.

5. **Dry-run the release workflow on the commit you are about to tag.** This is
   the same job that runs on a tag push — same build, same signing, same SBOMs —
   with `--snapshot`, so it publishes nothing:

   ```bash
   gh workflow run release.yml --ref main
   sleep 5 && gh run list --workflow=release.yml --limit 1   # grab the run id, then `gh run watch <id>`
   ```

   Do not skip it. **A pushed tag cannot be taken back**: the Go module proxy
   caches `vX.Y.Z` against that commit the moment the tag lands, so a release that
   dies partway through burns the version and the next one has to be `vX.Y.Z+1`.
   That is exactly how `v0.7.0` was lost. Signing is the step that most needs the
   rehearsal — it is the one part of the pipeline no local snapshot and no PR
   check exercises (see [below](#validating-the-config-locally)).

6. Tag the CLI on the commit that pins the SDK and push it — this starts the
   Release workflow (it filters to `v[0-9]*`, so only this tag triggers it):

   ```bash
   git tag -a vX.Y.Z -m "task-manager vX.Y.Z"
   git push origin vX.Y.Z
   ```

7. GoReleaser builds linux / macOS / Windows archives (amd64 + arm64), a
   `checksums.txt`, a **CycloneDX SBOM per archive**, a **keyless cosign signature
   over `checksums.txt`** (`checksums.txt.bundle`, which is why the workflow grants
   `id-token: write`), and a **draft** release named
   `task-manager vX.Y.Z` with a grouped changelog. Open the release, edit the notes
   if you want, and **publish**.

   > The release is a draft by default so notes can be curated before it goes out.
   > To publish automatically on tag push instead, set `draft: false` in
   > `.goreleaser.yaml`.

### Validating the config locally

GoReleaser config changes are checked on every PR that touches `.goreleaser.yaml`
or the workflow (a snapshot build, no publish). To run the same checks by hand:

```bash
goreleaser check                                          # validate .goreleaser.yaml
goreleaser release --snapshot --clean --skip=sign,sbom    # build every target into ./dist (no publish)
```

`--skip=sign,sbom` matches `release.yml`'s snapshot path: those steps need `cosign`
and `syft` on `PATH`, which snapshot validation does not install. Drop the flag only
if you have both.

**Signing is not covered by either of those.** Keyless cosign needs an `id-token`
from GitHub's OIDC provider, which no local run has and which the PR job must never
be given — it builds unreviewed code. So the signing step runs in exactly two
places: a real tag push, and the dry run of step 5 (`workflow_dispatch` on
`release.yml`, plus a weekly schedule that surfaces drift on its own). Run the dry
run and signing is verified while the version is still recoverable; skip it and the
tag push is the first execution.

> **The cosign binary is pinned** (`cosign-release` in `release.yml`), not just the
> installer action, so a new cosign cannot arrive unannounced. That is how `v0.7.0`
> failed: the config then passed `--output-signature` / `--output-certificate`,
> which the newly-released 3.x does not accept, and the run aborted on an empty
> `--bundle` path. The pin is now `v3.1.3` and the signature is one bundle; a
> cosign major is still a deliberate upgrade, because each one can change the
> published artifact and the verify command under [Verifying](#verifying).
> GoReleaser itself is still a floating `~> v2` constraint; the weekly dry run is
> what covers it.

## Verifying

The release carries the archives, `checksums.txt`, per-archive SBOMs, and
`checksums.txt.bundle`. Needs cosign 3.x. From a downloaded set:

```bash
# 1. Contents match the manifest
sha256sum -c checksums.txt

# 2. The manifest itself is authentically ours (keyless / Sigstore)
cosign verify-blob checksums.txt \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp '^https://github\.com/hk9890/task-manager/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The bundle carries the signature, the Fulcio certificate and the transparency-log
entry together, so it is the only file `verify-blob` needs.

Then confirm the binary and the module resolve as expected:

```bash
taskmgr version                                        # tagged version / commit / date
go get github.com/hk9890/task-manager/sdk@vX.Y.Z       # must resolve the sdk/vX.Y.Z tag
```
