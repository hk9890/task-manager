<!--
type: chore — a change that leaves behaviour identical.

Use when   the change leaves behaviour identical: cleanup, refactor, bump.
Inside     what is untidy and the cost it imposes now; the shape afterwards; the
           evidence that behaviour did not move — the tests or command output
           that must be unchanged.
Not inside a behaviour change smuggled along for the ride. The moment behaviour
           moves, it is a bug or a feature and gets its own issue.

A finished body to match the register is in taskmgr guide filing.

Delete this comment block before filing.
-->

## Context

<Every site involved, as file:line. A chore that names one site and means three
will be finished at one.>

## Problem

<What the duplication or the mess costs now — not in the abstract. The strongest
form is an incident it already caused: "the last change updated two of the three
and missed the third, caught in review rather than by a test".>

## Recommended action

<The shape afterwards, and the deletion that makes it a cleanup rather than an
addition. Say plainly that behaviour does not move.>

## Acceptance criteria

- [ ] <A grep or build command that proves the old shape is gone>
- [ ] <The test suite passes with no test file modified — that is what says
      behaviour held>
- [ ] <A byte-identical output comparison, where one exists>
