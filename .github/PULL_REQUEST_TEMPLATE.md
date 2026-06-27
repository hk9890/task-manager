## What & why

Briefly describe the change and the motivation.

## Checklist

- [ ] `make fmt vet test` passes locally (CI also runs `-race` and the
      `integration` tag).
- [ ] Behaviour changes are reflected in the relevant spec under `docs/specs/`.
- [ ] The change respects the single-writer rule (only `sdk/tasks` writes the
      store) — see [docs/CODING.md](../docs/CODING.md).
- [ ] PR is focused and the description explains the what and why.

## Related issues

Closes #
