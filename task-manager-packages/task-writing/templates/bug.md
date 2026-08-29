<!--
type: bug — behaviour that is wrong against what was intended.

Use when   observed behaviour differs from intended behaviour and you can state
           both halves.
Inside     the repro (exact steps or a command anyone can run); the observed
           output, pasted; the expected output; the anchor (file:line, endpoint,
           symbol) when you have it.
Not inside a diagnosis you have not verified. A confident wrong cause sends the
           implementer down it and costs more than no cause at all. If you have a
           hypothesis, mark it as one.

A finished body to match the register is in taskmgr guide filing.

Delete this comment block before filing.
-->

## Context

<file:line, endpoint or symbol. Where the reader starts. One or two lines. Add the
origin when it is evidence: reproduced on <branch> at <commit>.>

## Problem

<What the code does now, and the cost of leaving it. Paste the actual output, the
actual error, the actual failing assertion — not a description of it. Then state
what was expected.>

## Recommended action

<The smallest change that satisfies the criteria below. Name the approach if it
has been decided; say so if it has not.>

## Acceptance criteria

- [ ] <A command to run, or an observation someone who was not there can make>
- [ ] <The unbroken case still behaves as before>
- [ ] <The test command that now covers this path>
