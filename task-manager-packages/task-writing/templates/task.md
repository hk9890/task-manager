<!--
type: task — actionable work that is neither broken, nor new capability, nor pure
cleanup: an investigation, a spike, a migration step, a decision to be made.

Use when   the work is none of the other four.
Inside     the question to answer or the step to take, and the OUTPUT that ends
           it — a written finding, a decision recorded, a filed follow-up, a
           config change.
Not inside an open-ended "look into X". A task with no stopping condition never
           closes.

A finished body to match the register is in taskmgr guide
pkg:task-writing:bodies.

Delete this comment block before filing.
-->

## Context

<Where to look: the endpoint, the dashboard, the commit range, the two dates
being compared. Enough that someone else could start the same investigation.>

## Problem

<What is not known, and why it matters now. List the candidate causes if you have
them, marked as candidates.

State the stopping condition in one line: "This task ends with an answer, not a
fix.">

## Recommended action

<The method, concretely: what to run, against what data, in what order. Say where
to start and how to widen if the first pass finds nothing.>

## Acceptance criteria

- [ ] <The written output that ends it — a comment on this issue naming the
      finding, with the numbers that show it>
- [ ] <What happens if the answer is yes: a follow-up filed and linked>
- [ ] <What happens if the answer is no: what was measured, and the next
      hypothesis>
