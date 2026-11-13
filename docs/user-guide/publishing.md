# Publishing your stores

This page covers publishing this machine's central stores to a private GitHub repository.
Each machine publishes to its own branch, and nothing else writes to that branch.

Only central stores are published — the ones under `<central_root>/stores/`, by default
`~/.taskmgr/stores/` ([Where your tasks live](stores.md)). A project's own `.tasks/` already travels with that
project's repository.

## What you get, and what you do not

- **A mirror, not a shared tracker.** The repository is a copy to read. Change a ticket on
  the machine that owns its store; the next `taskmgr sync` publishes the change.
- **One branch per machine:** `machine/<machine-id>`. Two machines never write to one
  branch, so a sync never merges and never needs your attention.
- **Full history.** Every sync that finds a change is one commit, so the branch log is the
  history of your stores.

## Set it up once per machine

You need `git` installed. The steps below use the GitHub CLI, `gh`; any git remote you can
push to works the same way.

1. **Name the machine.** The default is the hostname, lowercased and cut at the first
   dot: `Hans-Laptop.local` becomes `hans-laptop`. Set a name to be sure two machines
   never share one:

   ```bash
   taskmgr config set machine_id laptop --global
   ```

   A name is lowercase letters, digits and `-`, starting with a letter or digit, at most
   63 characters.

2. **Create a private repository**, once, from any machine:

   ```bash
   gh repo create taskmgr-stores --private
   ```

3. **Point the stores at it:**

   ```bash
   taskmgr sync init --remote git@github.com:<you>/taskmgr-stores.git
   ```

   This makes `<central_root>/stores/` a git repository on the branch `machine/laptop`.
   The lock and temp files `taskmgr` creates while it writes are never published; it adds
   no file to your stores for that. Running it again with the same URL changes nothing.

4. **Publish:**

   ```bash
   taskmgr sync
   ```

Repeat steps 1, 3 and 4 on every other machine, with its own name and the same URL.
`taskmgr sync init --help` and `taskmgr sync --help` list the switches.

To look at what a machine published, browse its `machine/<machine-id>` branch on GitHub,
or clone it: `git clone --branch machine/laptop <url>`.

## Everyday use

Run `taskmgr sync` whenever you want the mirror up to date. It commits what changed and
pushes it. With nothing new it prints `Nothing to publish: no change since the last sync` and does nothing, not even a network call. `--json` reports
what it did, for a script that runs it.

A sync never publishes a half-written ticket: while it commits, every write to your
central stores waits, for as long as the local commit takes. When a write is already in
progress, sync waits for it to finish and blocks nothing while it waits. The push to the remote does
not hold anyone up, and two syncs started at the same time run one after the other. Your
tickets on this machine are never changed; sync only reads them.

Every sync commit is made as `taskmgr`, with the e-mail `<machine-id>@taskmgr.invalid`,
whatever git identity your machine has. Your own name and e-mail never reach the
repository.

## When sync refuses

| Message says | What happened | What to do |
|---|---|---|
| not set up for sync | `sync init` has not run on this machine | run `taskmgr sync init --remote <url>` |
| has no remote "origin" | the remote was removed after `sync init` | run `taskmgr sync init --remote <url>` |
| no machine ID can be derived from the hostname | this machine's hostname gives no usable name | set one with `taskmgr config set machine_id <id> --global` |
| is not on a branch | the stores' git work tree was checked out on a single commit by hand | check out `machine/<machine-id>` again with the `git checkout` the message prints |
| another machine publishes under this machine ID | two machines use one name, or this machine was set up again from scratch | pick a new name with `taskmgr config set machine_id <id> --global`, run the `git branch -m` the message prints, then `taskmgr sync`. git's own message follows, naming what the remote refused |
| machine_id changed since 'taskmgr sync init' | you renamed the machine after setting it up | set the old name back, or run the `git branch -m` the message prints |
| sync publishes only from machine/... | the stores were already a git repository on another branch, such as `main`, or a `sync init` stopped part-way | run what the message prints: `sync init` again, or a `git checkout` or `git branch -m` |
| git is not on PATH | git is not installed, or not on this shell's `PATH` | install git |
| could not run git | git is installed but could not start, for the reason the message gives | fix that reason, such as the permissions of the stores directory |
| already publishes to | `sync init` was given a different URL from the first time | to publish to the new URL, run the `git remote remove origin` the message prints, then the same `sync init` again |
| is not a valid machine ID | the name breaks the rule in step 1 | set a valid name |

`sync` never overwrites what is on the remote. When it refuses, the remote still holds
everything it held before.
