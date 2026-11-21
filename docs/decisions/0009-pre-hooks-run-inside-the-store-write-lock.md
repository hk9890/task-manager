# 0009: Pre-hooks run inside the store write lock, before the write

A pre-hook is a gate: a script that can refuse a change. Inside the lock it decides against exactly
the state that will be written, gets the issue before and after the change as one atomic snapshot,
and its denial is atomic. The cost: no other write to the store proceeds until it returns.

Rejected: a pre-hook outside the lock, because the store could move under it, so the gate would
decide against state that is no longer the state being written.
