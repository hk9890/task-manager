Before the close, the issue's own acceptance criteria are the checklist. For what
a body carries: taskmgr guide pkg:task-writing:types

## Check every criterion

Read them from the issue rather than from memory of the work:

  taskmgr show <id> --json | jq -r .description

Each is written to be runnable by someone who was not there, so run it and read
the result. Believing a criterion holds is not checking it.

The cost of getting this wrong is not local. Closing is what releases the issues
this one blocked, so a false close puts dependent work into ready on a floor that
was never laid — and the issue is now in the cold partition, where nobody reads
it again.

## Report what you cannot check

A criterion you cannot run — no environment, no credentials, not testable as
written — is reported, never assumed. Name it, say why, and leave the issue open.
A criterion that fails is the same answer arrived at differently: it is a result,
and the result is that the issue is not done.

  bad:   4 of 5 criteria pass, the fifth needs staging — closing it anyway
  good:  4 of 5 pass. The fifth needs a staging token I do not have, so I have
         not run it. Left open.

Closing is one command and reopening is another, but the release of the blocked
issues has already happened by then, and whoever picked one up has already
started.

## --reason names the evidence

The reason is what explains the close to whoever reads the history next, so state
what was checked and what it printed, not that it is done:

  bad:   --reason "done"
  bad:   --reason "implemented as described"
  good:  --reason "All 3 criteria pass: expired-token curl returns 401, valid
         returns 200, npm test -- auth green. Fixed in a1b3f90."

It holds up to 4096 characters and may be multi-line, so there is room for the
commit, the command and the output it gave.
