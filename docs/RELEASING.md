# Releasing

agent-tasks has two release artifacts: the **`atctl` CLI binary** and the
**`sdk/tasks` Go module**. There is no automated release pipeline yet — releases
are cut manually with `make` and git tags. (A GoReleaser-based flow, like the one
in beads-workbench, is the intended direction once CI is set up.)

## Versioning

- Semantic version tags: `vX.Y.Z`.
- `atctl --version` is stamped at build time via `-ldflags` into `cmd.Version` /
  `Commit` / `Date` (see the `makefile`). Untagged local builds report `dev`.
- The SDK is a **separate Go module** (`sdk/`). Go consumers pin it with a
  module-path tag, `sdk/vX.Y.Z` (`go get …/sdk@vX.Y.Z`). Keep it in step with the
  CLI tag.

## Cutting a release

Run from the repository root on a clean, up-to-date tree.

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

4. Tag the CLI and the SDK module on the release commit:

   ```bash
   git tag -a vX.Y.Z     -m "agent-tasks vX.Y.Z"
   git tag -a sdk/vX.Y.Z -m "sdk/tasks vX.Y.Z"
   ```

5. Push the tags:

   ```bash
   git push origin vX.Y.Z sdk/vX.Y.Z
   ```

6. Build the binary artifact (version stamped from the tag):

   ```bash
   make build      # -> ./bin/atctl
   ```

   Cross-platform archives are a future GoReleaser step.

## Verifying

```bash
./bin/atctl version          # shows the tagged version / commit / date
```

From a consumer, `go get github.com/hk9890/agent-tasks/sdk@vX.Y.Z` must resolve
the `sdk/vX.Y.Z` tag.

## Visibility

The repository is private; releases are internal-only unless a maintainer decision
changes that.
