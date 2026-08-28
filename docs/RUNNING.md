# Running

**Local delta —** use the `run` skill for the generic launch-and-drive flow; everything
below is this repository's delta and wins where the two disagree. What each command *does*
is `taskmgr <command> --help` and [user-guide/](user-guide/); the automated suites are
[TESTING.md](TESTING.md)'s, and reading a captured run is [MONITORING.md](MONITORING.md)'s.

## Run the checkout, never the installed release

A `taskmgr` on `PATH` is whatever `make install` or `go install` last put there — not this
working tree.

```bash
go build -o bin/taskmgr ./cmd/taskmgr   # then invoke ./bin/taskmgr
mise run taskmgr -- ready               # builds first, then runs in YOUR shell's directory
```

`mise run taskmgr` is the one that respects where you are standing; a plain `mise run`
task executes at the project root, which is the wrong store for a reproduction.

## Point every run at a scratch store first

`taskmgr` resolves a store before it does anything else, and **both fallbacks reach real
data**:

- **Walk-up** finds the first `.tasks/` at or above the working directory. A worktree under
  `.claude/worktrees/` sits *inside* the repository, so a run from one walks up into the
  main checkout and operates on its store.
- **The central registry** answers when no `.tasks/` is found — that is `~/.taskmgr/`, your
  own tasks.

So isolate both halves before running anything that writes:

```bash
export TASKMGR_HOME=$(mktemp -d)        # a throwaway registry; the real ~/.taskmgr is untouched
scratch=$(mktemp -d)
./bin/taskmgr -C "$scratch" init --prefix demo
./bin/taskmgr -C "$scratch" create --title "First task" --type feature --priority 1 --json
./bin/taskmgr -C "$scratch" where       # prints kind/store/project — confirm before you write
```

`where` never errors and never writes; run it whenever you are unsure which store a
directory resolves to.

## Reproduce a reported bug

1. Build the checkout, and isolate as above.
2. Rebuild the reporter's store shape with `init` + `create` — a store is plain files, so a
   fixture is a few commands, and the state is inspectable with `cat` between steps.
3. Replay the exact invocation, flags included. `--json` is the stable surface; the human
   table is not.
4. Read the exit code and stderr together: every error is `taskmgr: <message>` on stderr
   with an empty stdout and exit `1`. `TASKMGR_LOG=debug` adds structured records on the
   same stream ([MONITORING.md](MONITORING.md)), and `--json` on stdout stays clean of both.

## Drive a hook by hand

A hook is the only path that runs code this repository did not write, so it is the one
reproduction with a moving part. A hook lives in a package, so write one into the scratch
store, point the store at it, then trigger the transition:

```bash
mkdir -p "$scratch/.tasks/packages/probe"
cat > "$scratch/.tasks/packages/probe/taskmgr-package.yaml" <<'EOF'
version: 1
hooks:
  - id: probe
    event: pre-close
    run: ["sh", "-c", "echo refused >&2; exit 1"]
EOF
./bin/taskmgr -C "$scratch" package add --path packages/probe
./bin/taskmgr -C "$scratch" hook list           # the effective chain, in run order
./bin/taskmgr -C "$scratch" close <id>          # exits 1, prints the hook's reason
./bin/taskmgr -C "$scratch" close <id> --json   # the structured hook_denied error
```

The denial names the hook `pkg:probe:probe`, so a reproduction that spans several
packages still says which one refused.

A write that hangs is a pre-hook running: it holds the store lock until
`hook_timeout` ends it.
