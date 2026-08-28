# Documenting

**Local delta —** the generic standard is the `instruction-writing:writing-project-docs`
skill: which file owns what, and how a project doc is written. This file records only the
delta, and wins where the two disagree.

## The gate

`mise run check:docs` (`scripts/checkdocs`) runs in `quality`, `quality:full`, and the CI
lint job. It rejects four things:

- a `cmd/…` or `sdk/…` path that does not exist on disk;
- a citation pinned to a line number — `path:42` and `path#L42` both fail. **Cite the
  symbol**: it is greppable, which a line number is not;
- a relative Markdown link whose target file, or whose `#anchor` inside that file, does
  not resolve;
- a `docs/user-guide/` page that cites the source tree or links to a spec.

The gate reads fenced blocks like any other text: a stale path in a code block misleads a
reader exactly as far as one in prose. What it cannot see is everything below.

## `docs/specs/`

When a spec is updated, and which one, is
[CODING.md § Keep specs in sync](CODING.md#keep-specs-in-sync)'s. What goes in it:

- Specify data, contract and algorithm: file formats and their keys, the command surface,
  the resolution rule, the grammar, the hook payload.
- Which spec owns a subject is [OVERVIEW.md § Specifications](OVERVIEW.md#specifications)
  — read it before placing a sentence.
- Test every sentence against a second front end. A rule that only holds because the
  caller is a terminal belongs to the CLI spec, not to the storage, config or query spec.
- Explain a trade-off where the design would otherwise read as arbitrary, and say what was
  rejected. A spec that only states the rule invites the next reader to "simplify" it back.

## `docs/user-guide/`

- Write for someone who installed `taskmgr` and will never open this repository.
- Name a file path only where the user creates or edits it themselves — `.tasks/config.yaml`,
  `~/.taskmgr/mapping.yaml`. A Go source path, a `mise` task, a build tag or a spec is a
  developer concern and the gate rejects it.
- `taskmgr <command> --help` and `taskmgr commands` are the flag and verb roster. No page
  repeats one; a page says what to do and links the reader to `--help` for the switches.
- Every page opens by saying what it covers, so the index can send a reader straight to it.

## Decisions

What this repository has decided not to document, and why. Re-open one here rather than
filling the gap somewhere else.

- **`cmd/guide.go` does not grow.** The binary ships a hand-maintained how-to
  (`taskmgr guide`) so an agent in a terminal needs no files at all; that is why it is
  plain text, links nowhere, and stays short. It overlaps [user-guide/](user-guide/) on the
  issue model and the everyday loop, deliberately — a user guide that omitted them would
  not be a guide. The rule that keeps the two from drifting apart is one-directional: new
  user-facing prose goes to `user-guide/`, and `guideText` only ever changes to stay true.
- **No prose command reference.** `taskmgr commands` is derived from the live command tree
  and cannot drift; `--help` carries the flags; [CLI-SPEC](specs/CLI-SPEC.md) is the
  normative contract. A fourth copy in a user-guide page would be the only one that could
  be wrong.
- **No `CHANGELOG.md` entries.** GoReleaser generates the grouped changelog onto the
  GitHub release from the merged PRs of each `vX.Y.Z` tag. The tracked file stays a
  pointer at the Releases page, not a hand-maintained log that would repeat it.
- **No ADR directory.** An architecture decision is filed as a `taskmgr` issue and cited by
  ID from the spec it constrains — [ARCHITECTURE-SPEC §7](specs/ARCHITECTURE-SPEC.md) cites
  `at-39dru2` for the open-vs-closed partition axis. The decision then sits beside the rule
  it explains instead of in a parallel tree nobody opens.
- **[implementation/](implementation/) owns nothing**, and says so at the top of each file.
  It exists because [ARCHITECTURE-SPEC §5](specs/ARCHITECTURE-SPEC.md) is normative and long,
  and an agent orienting needs one screen. Promoting either file would give the package
  table two homes.
