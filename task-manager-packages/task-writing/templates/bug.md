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

Worked example, for the register to match:

  ## Context
  src/middleware/auth.ts:42, verifyToken. Reported by a user whose session still
  worked eight days after signing out on another device; reproduced on main at
  a1b3f90.

  ## Problem
  verifyToken reads the exp claim but never compares it to the current time, so
  any structurally valid token authenticates indefinitely:

      $ curl -s -o /dev/null -w '%{http_code}\n' \
          -H "Authorization: Bearer $EXPIRED_TOKEN" localhost:3000/export
      200

  Expected 401. Sessions cannot be ended — expiry is the only revocation
  mechanism the service has, and it does nothing.

  ## Recommended action
  In verifyToken, compare exp against the current time and reject when it is in
  the past. Return 401 with the existing TokenExpired body; no new error type, no
  change to the refresh flow.

  ## Acceptance criteria
  - [ ] The curl above returns 401
  - [ ] The same request with an unexpired token still returns 200
  - [ ] npm test -- auth passes, including a new case for the expired path

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
