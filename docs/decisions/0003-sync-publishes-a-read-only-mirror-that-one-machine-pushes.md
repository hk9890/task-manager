# 0003: `taskmgr sync` publishes the central stores of a machine as a read-only mirror, on a git branch that only this machine pushes

One writer per branch makes every push a fast-forward, so nothing is merged. The cost: a mirror
cannot be edited where it is read.

Rejected: merging tickets across machines, because two writers to one issue need a merge rule per
field and per file of the issue, and a store on two machines no longer has one writer.

Rejected: a server, because it is a daemon, an account and an uptime to own.

Rejected: a file-sync tool such as Syncthing or Dropbox, because every copy is writable, and a
concurrent change becomes a conflict copy the engine never reads.

Rejected: git `merge=union`, because a field edited on both sides comes out with both values.
