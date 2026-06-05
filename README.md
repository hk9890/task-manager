# agent-tasks

A lean, file-based task tracker — issues, dependencies, and ready-work, and
nothing else. No database: every task is a Markdown file under a `.tasks/`
directory, versioned alongside your code. You work with it through
**`atctl`**, the command-line tool.

## Install

From a checkout of this repo (Go 1.26+):

```bash
make install      # builds `atctl` and puts it on your $PATH
```

## Usage

```bash
cd your-project
atctl init --prefix proj                 # create .tasks/
atctl create --title "First task" --type task --priority 1
atctl create --title "Depends on it" --blocked-by proj-0001
atctl ready                              # what can I work on now?
atctl show proj-0001
atctl update proj-0001 --status in_progress
atctl close proj-0001 --reason "done"
atctl ready                              # the dependent is now ready
```

Add `--json` to any command for machine-readable output.
