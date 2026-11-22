# 0011: A configuration takes hooks only from the packages it names

A package is a directory that holds hooks and their scripts, so the relative path of the program a
hook runs keeps its meaning when the package is copied. A config file declares no hook, so the
store file and the per-user file merge as two lists of packages.

Rejected: an inline `hooks:` block in a config file, because a hook runs from the project root, so
an inline entry could name a script only by absolute path, which is machine-specific.
