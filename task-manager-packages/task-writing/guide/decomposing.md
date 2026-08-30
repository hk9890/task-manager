Turning a review, a plan or a spec into a filed set. For the body each issue
carries: taskmgr guide filing

## Ground it first

Open the code each issue will touch and take the real path, the real symbol, the
real command from it. An issue written from the discussion carries the
discussion's vocabulary; one written from the code carries the project's, and the
implementer starts in the right file instead of searching for it.

## One issue per what

Findings — a review, a failing build, a cleanup pass. One issue per finding.
Several instances of the same fix are one issue, not one per site. A finding that
is both a defect and untidiness around it splits by type: the defect is a bug, the
cleanup a chore. Batching them hides one behind the other's done-state.

A plan or a spec — cut it into tracer bullets. Each issue is a narrow but complete
path through every layer it touches — schema, API, UI, test — so finishing it
produces something demonstrable. Size each to one sitting.

  bad:   Add the export_jobs table and its migration
  good:  Export one order as CSV from /export/orders, end to end

The bad one is a horizontal slice: it can close with nothing working, so the first
evidence anything is right arrives only after the last issue in the set closes.

## Real edges only

blocked-by is an ordering constraint that exists: B cannot start until A closes.
Preferring to do A first is priority, not a blocker. A false edge makes ready lie
about what is available, which is the one thing ready is for.

parent groups children under an epic that share one outcome. It is organisational
and blocks nothing.

## Approve before filing

Present the whole set before creating anything — id-less, one line each, carrying
the type, the priority and the edges:

  1. [epic]      Orders CSV export
  2. [bug/1]     verifyToken accepts a token whose exp is in the past
  3. [feature/2] Export the orders table as CSV   parent: 1  blocked by: 2

Ask about granularity, the edges, and anything to merge, split or drop. A filed
set is harder to reshape than a list in a message: every change costs a command
afterwards and a sentence before.

## Report what landed

Id, type, priority and title for each, and the edges between them, so the reader
can see the shape and run taskmgr ready. Name anything you did not file and why —
outside the scope you were given, or no testable criterion could be written for
it. Saying so is the report; filing a wish to cover it is not.
