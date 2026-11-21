# 0004: A store partitions issues into open and closed only

Closed issues dominate a mature store (often about 90%). Keeping them in `closed/` makes a scan of
the active work cost O(open) and keeps it flat as history grows.

Rejected: a `parked/` partition for deferred or long-parked issues, because the scan costs about
13µs per open issue, so a split by status adds routing complexity for no correctness benefit.
