# 0010: A gate on create, update, close or reopen has no bypass, and a pre-hook that cannot allow denies

A gate is a pre-hook: a script the engine runs before a transition, which can deny it. Hooks exist
so that an autonomous agent cannot silently skip policy. A misconfigured pre-hook blocks the
mutations it gates until it is fixed; that is intended.

Rejected: a bypass or skip flag, because a gate that can be skipped is not a gate.

Bulk import is a distinct call that omits hooks unless the caller asks.
