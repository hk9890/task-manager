# 0007: Policy lives in hooks, and the engine holds none

A behaviour that can be a hook, a script the engine runs at an issue state transition, is a hook
and not engine code. That split lets the engine stay small and still support complex,
project-specific workflows.

Rejected: policy baked into the engine, such as "tests must pass before close", because it is
unused weight in the core.
