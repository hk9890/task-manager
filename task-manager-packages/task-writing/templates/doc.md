<!--
type: doc — not work at all. It carries a document: a design page, a session
note, a handover, a review.

Use when   the issue IS a document rather than something to be done.
Inside     the document, under its own headings. The four work sections do not
           apply. Which kind of document it is goes in a label — kind:design,
           kind:session, kind:handover — because the type does not say.
Not inside anything to do. A design page that ends in "and then build it" buries
           work in the one type the ready queue cannot see. File that work
           separately, at the type the work picks, and link the two:
             taskmgr rel add <doc-id> <task-id>

taskmgr holds doc out of ready and blocked by construction, so a doc's status,
priority and assignee are stored but mean nothing. Reach one by asking:
  taskmgr list -q 'type == "doc"'

No gate checks a doc's body. The headings below are one shape that works for a
design page — replace them with the ones your document actually needs.

A finished body to match the register is in taskmgr guide
pkg:task-writing:bodies.

Delete this comment block before filing.
-->

## Decision

<What was decided, in the imperative. One paragraph.>

## Why

<The evidence. Paste the measurement, the failure, the output that forced the
decision. This is the section a reader in six months comes back for.>

## Consequences

- <What gets worse, stated plainly. A page with only upsides was not a decision.>
- <What a reader has to do differently now.>

## Rejected

<The alternative that was seriously considered, and the specific reason it lost.
Without this the next reader re-proposes it.>
