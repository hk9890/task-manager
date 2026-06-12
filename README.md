# agent-tasks

A lean, file-based task tracker — issues, dependencies, and ready-work, and
nothing else. No database: every task is a Markdown file under a `.tasks/`
directory, versioned alongside your code. You work with it through
**`taskmgr`**, the command-line tool.

## Install

From a checkout of this repo (Go 1.26+):

```bash
make install      # builds `taskmgr` and puts it on your $PATH
```

## Usage

```bash
cd your-project
taskmgr init --prefix proj                 # create .tasks/
taskmgr create --title "First task" --type task --priority 1
taskmgr create --title "Depends on it" --blocked-by proj-0001
taskmgr ready                              # what can I work on now?
taskmgr show proj-0001
taskmgr update proj-0001 --status in_progress
taskmgr close proj-0001 --reason "done"
taskmgr ready                              # the dependent is now ready
```

Add `--json` to any command for machine-readable output.
