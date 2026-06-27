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
  `.goreleaser.yaml`); a local `make build` stamps them from `git describe`.
  Untagged local builds report `dev`.
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

2. Green gate:

   ```bash
   make fmt vet test
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
   git commit -am "chore: pin sdk vX.Y.Z"
   ```

5. Tag the CLI on the commit that pins the SDK and push it — this starts the
   Release workflow (it filters to `v[0-9]*`, so only this tag triggers it):

   ```bash
   git tag -a vX.Y.Z -m "task-manager vX.Y.Z"
   git push origin vX.Y.Z
   ```

6. GoReleaser builds linux / macOS / Windows archives (amd64 + arm64), a
   `checksums.txt`, and a **draft** release named `task-manager vX.Y.Z` with a
   grouped changelog. Open the release, edit the notes if you want, and **publish**.

   > The release is a draft by default so notes can be curated before it goes out.
   > To publish automatically on tag push instead, set `draft: false` in
   > `.goreleaser.yaml`.

### Validating the config locally

GoReleaser config changes are checked on every PR that touches `.goreleaser.yaml`
or the workflow (a snapshot build, no publish). To run the same checks by hand:

```bash
goreleaser check                       # validate .goreleaser.yaml
goreleaser release --snapshot --clean  # build every target into ./dist (no publish)
```

### Building locally (development)

`make build` still produces a single host binary for local use:

```bash
make build      # -> ./bin/taskmgr
```

## Verifying

```bash
./bin/taskmgr version          # shows the tagged version / commit / date
```

After the release publishes, the archives + `checksums.txt` are attached to the
GitHub release. From a consumer, `go get github.com/hk9890/task-manager/sdk@vX.Y.Z`
must resolve the `sdk/vX.Y.Z` tag.

## Visibility

The repository is public. Releases are published as GitHub Releases with
cross-platform archives and a `checksums.txt`; the `sdk/tasks` module is fetched
straight from its tag via the Go module proxy.
