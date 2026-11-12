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

   This makes `<central_root>/stores/` a git repository on the branch `machine/laptop`, and
   writes a `.gitignore` there that keeps out the lock and temp files `taskmgr` creates
   while it writes. Running it again with the same URL changes nothing.

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
pushes it. With nothing new it does nothing, not even a network call. `--json` reports
what it did, for a script that runs it.

A sync that runs while a ticket is being written can publish a half-written state. That is
harmless: the next sync publishes the finished one.

## When sync refuses

| Message says | What happened | What to do |
|---|---|---|
| not set up for sync | `sync init` has not run on this machine | run `taskmgr sync init --remote <url>` |
| another machine publishes under the machine ID | two machines use one name, or this machine was set up again from scratch | pick a new name with `taskmgr config set machine_id <id> --global`, run the `git branch -m` the message prints, then `taskmgr sync` |
| machine_id changed since 'taskmgr sync init' | you renamed the machine after setting it up | set the old name back, or run the `git branch -m` the message prints |
| already publishes to | `sync init` was given a different URL from the first time | change the URL with `git remote set-url`, as the message shows, if that is what you want |
| invalid machine_id | the name breaks the rule in step 1 | set a valid name |

`sync` never overwrites what is on the remote. When it refuses, the remote still holds
everything it held before.
