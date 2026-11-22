# 0008: A hook gates or notifies, and the engine changes no issue from hook output

The engine writes exactly what it validated and stays the sole author of on-disk state. A hook
that runs before a change denies it with a reason, and the caller, mainly an autonomous agent,
makes the change and retries: "gate, don't fix".

Rejected: a hook whose output writes labels or other fields, although auto-labeling is a common
tracker hook, because the engine is then no longer the only writer, and the validation and
atomicity guarantee rests on that.
