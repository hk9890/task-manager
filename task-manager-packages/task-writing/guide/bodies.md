A task is read cold. Whoever executes it — a colleague next month, an agent with
no transcript — has the body and the repository, nothing else.

A task that names a wanted end-state but no executable path is a wish. Wishes
file cleanly and read fine, and they cost the implementer a round trip before any
work starts. They are the default failure, not a rare one.

## Which type carries it

The nature of the work picks the type. Where it was discovered picks nothing.

  bug      behaviour that is wrong against what was intended
  feature  a capability that does not exist yet
  chore    a change that leaves behaviour identical — cleanup, refactor, bump
  task     actionable work that is none of the above — an investigation, a
           migration step, a decision to be made
  epic     a container for other issues; it holds no implementation of its own
  doc      not work at all — a design page, a session note, a handover, a review

When a finding is both a defect and untidiness around it, the defect is a bug and
the cleanup a separate chore. Batching them hides one behind the other's
done-state. Several instances of the same fix are one issue, not one per site.

## The four sections

A bug, a feature, a chore and a task carry the same four, same names, same order:

  ## Context
  ## Problem
  ## Recommended action
  ## Acceptance criteria

**Context** — where. The anchor the reader starts from: path/file.ext:line, an
endpoint, a command, a symbol. One or two lines. It records the origin when that
is load-bearing evidence ("reproduced on main at a1b3f90") and stays silent when
it is not.

**Problem** — what is wrong and what it costs. The observed behaviour, stated
concretely, and the consequence of leaving it. This section earns the issue its
priority; a Problem that cannot explain the cost is one nobody will pick up.

**Recommended action** — the change to make, at the smallest scope that satisfies
the criteria. Name the approach when it has been decided, and say so when it has
not ("either X or Y; decide during implementation"). Adjacent work goes to its
own issue, linked — not into a parenthetical here.

**Acceptance criteria** — how the implementer knows they are finished. A
checklist, each item a command to run or an observation to make. A verifier
executes it, so it doubles as the test plan.

An **epic** carries Context, Outcome, Success criteria — never an implementation.
A **doc** carries no prescribed sections: its body is the document, under the
document's own headings.

## The six rules

**Cold start** — nothing may point back at the conversation that produced it. No
"as discussed", no "the issue we saw yesterday", no pronoun whose referent was a
message. Names, paths and commands survive; scrollback does not.

**One problem** — one problem, one done-state. If the body needs "and also", it
is two issues. The test: could half of it be closed while the other half is still
open? Then split it.

**Evidence** — write what you observed, not what you concluded. Paste the actual
output, the actual error, the actual failing assertion. A described symptom
("the request seems to hang") costs a reproduction nobody needed to do.

**Testable done** — every criterion is runnable or observable by someone who was
not there.

  good:  GET /export with an expired token returns 401
  good:  npm test -- auth passes, including a case for the expired path
  bad:   Token handling works correctly
  bad:   The code is cleaner

If you cannot write one testable criterion, the issue is not ready to file. Say
what is missing instead of inventing one: an invented criterion gets verified,
passes, and closes work that was never done.

**Smallest change** — the Recommended action bounds the work. An implementer
follows what is written; a body describing a rewrite when a three-line fix would
do will get the rewrite.

**Economy** — every line changes what the implementer does. Restating what the
code obviously does, re-explaining the architecture, and hedging are load with no
effect. Cut whole sentences rather than trimming words from them.

## The wish test

Read the draft as the cold reader and answer two questions from the page alone:
where do I start, and how will I know I am done? An answer that lives in your
head, or in the conversation that produced the issue, means it is a wish. Repair
it before filing. A doc is exempt — it holds a document, not a path through work.

## What the gate refuses

The hooks in this package check the sections above on create and on update:

  pkg:task-writing:body-sections    bug, feature, chore, task
  pkg:task-writing:epic-sections    epic

A refusal names the missing section and exits non-zero; nothing is written. Docs
are never gated. The check is structural — it sees that "## Acceptance criteria"
is there with at least one "- [ ]" item, not whether the criteria are any good.
Clearing the gate is the floor, not the bar.

## Templates

One per type, with the contract for that type at the top:

  <package>/templates/{bug,feature,chore,task,epic,doc}.md

`taskmgr package list` prints the package directory this store resolved.
