This store gates issue bodies. A bug, feature, chore or task is refused unless
its description carries these four headings:

  ## Context               where: path/file.ext:line, an endpoint, a command
  ## Problem               what is wrong, stated concretely, and what it costs
  ## Recommended action    the change to make, at the smallest scope that works
  ## Acceptance criteria   a "- [ ]" checklist, each item runnable by someone
                           who was not there

An epic carries ## Context, ## Outcome, ## Success criteria instead, and holds no
implementation of its own. A doc is never gated: its body is the document, under
the document's own headings.

Acceptance criteria must hold at least one "- [ ]" item. The gate is structural —
it sees that the heading is present and the list is not empty, never whether the
criteria are any good. Clearing it is the floor, not the bar:

  good:  GET /export with an expired token returns 401
  good:  npm test -- auth passes, including a case for the expired path
  bad:   Token handling works correctly

If you cannot write one testable criterion, the issue is not ready to file. Say
what is missing instead of inventing one: an invented criterion gets verified,
passes, and closes work that was never done.

Write for a cold reader — a colleague next month, an agent with no transcript.
Names, paths and commands survive; the conversation that produced the issue does
not. Paste the output you saw rather than describing it.

A finished body:

  ## Context
  src/middleware/auth.ts:42, verifyToken. Reported by a user whose session still
  worked eight days after signing out; reproduced on main at a1b3f90.

  ## Problem
  verifyToken reads the exp claim but never compares it to the current time, so
  any structurally valid token authenticates indefinitely:

      $ curl -s -o /dev/null -w '%{http_code}' \
          -H "Authorization: Bearer $EXPIRED_TOKEN" localhost:3000/export
      200

  Expected 401. Expiry is the only revocation this service has, and it does
  nothing.

  ## Recommended action
  In verifyToken, compare exp against the current time and reject when it is in
  the past. Return 401 with the existing TokenExpired body; no new error type,
  no change to the refresh flow.

  ## Acceptance criteria
  - [ ] The curl above returns 401
  - [ ] The same request with an unexpired token still returns 200
  - [ ] npm test -- auth passes, including a new case for the expired path

A refusal names the section that is missing and writes nothing.

Choosing the type, the rules a good body clears, and a template per type:
taskmgr guide pkg:task-writing:types
