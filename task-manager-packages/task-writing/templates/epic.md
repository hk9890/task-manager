<!--
type: epic — a container for other issues. It holds no implementation of its own
and is never assigned to an implementer; its children are.

Use when   several issues share one outcome worth tracking on its own.
Inside     the outcome in user-visible terms; its own success criteria; the
           children.
Not inside implementation, and "all children are closed" as the success
           criterion. Children closing is a precondition, not evidence the
           outcome was reached — an epic can have every child closed and still
           not deliver what it promised.

An epic replaces Problem and Recommended action with Outcome and Success
criteria. The gate pkg:task-writing:epic-sections checks for these four.

A finished body to match the register is in taskmgr guide
pkg:task-writing:bodies.

Delete this comment block before filing.
-->

## Context

<The area this covers, and where the outcome was decided — the review, the
quarter's goal, the incident.>

## Outcome

<What is true for a user once this is done. Two or three sentences, no
implementation.>

## Success criteria

<Checked against the shipped product, not against the children's status.>

- [ ] <A user-visible capability, observable end to end>
- [ ] <A measure that shows the outcome was reached, with its window>
- [ ] <The load or scale case that has to hold>

## Children

- <one line per child; file each as its own issue and set --parent to this one>

## Not in this epic

<The neighbouring work raised at the same time and deliberately excluded. Say
what would have to be true for it to be picked up.>
