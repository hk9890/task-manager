Which type carries the work, and the rules a body clears once its four sections
are in place. For the sections themselves, and what the gate refuses:
taskmgr guide filing

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

## The six rules

**Cold start** — the body stands on names, paths and commands alone. Those
survive; scrollback does not. Every pronoun needs a referent on the page.

**One problem** — one problem, one done-state. If the body needs "and also", it
is two issues. The test: could half of it be closed while the other half is still
open? Then split it.

**Evidence** — write what you observed, not what you concluded. Paste the actual
output, the actual error, the actual failing assertion. A described symptom
("the request seems to hang") costs a reproduction nobody needed to do.

**Testable done** — every criterion is runnable or observable by someone who was
not there.

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

## Templates

One per type, holding that type's contract and a blank body to fill in. A package
cannot know where it was installed, so ask this store where it put this one:

  dir=$(taskmgr package list --json | jq -r '.[]|select(.name=="task-writing")|.path')
  cat "$dir/templates/bug.md"      # or feature, chore, task, epic, doc

## Done means

Every section held against its contract and against the six rules — cleared,
rewritten, or cut — and the wish test answered from the page alone.

A doc is done when the rules that survive its exemptions are clear: Cold start,
Evidence and Economy bind it; One problem, Testable done and Smallest change
describe work and do not.

Reading this is not the work. Applying it to the body in front of you is.
