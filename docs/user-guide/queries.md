# Filtering and search

`taskmgr list -q '<expression>'` selects issues with a small predicate language. This page
is the reference for it: every field, every operator, and the four things that surprise
people.

## The shape

A predicate is `<field> <operator> <value>`. Combine predicates with `&&`, `||`, `!` and
parentheses:

```bash
taskmgr list -q 'status == "open" && priority <= 1'
taskmgr list -q 'type == bug && label ~ "area:db"'
taskmgr list -q 'ready && priority <= 2'
taskmgr list -q 'assignee == "hans" && (type == bug || type == chore)'
taskmgr list --all -q 'closed > "2026-01-01"'
```

`!` binds tightest, then `&&`, then `||`. Parentheses override.

## Fields

| Field | Operators | Values |
|---|---|---|
| `status` | `==` `!=` | `open` `in_progress` `blocked` `deferred` `closed` |
| `type` | `==` `!=` | `task` `bug` `feature` `epic` `chore` `doc` |
| `priority` | `==` `!=` `<` `<=` `>` `>=` | `0`–`4` |
| `assignee` | `==` `!=` `~` | text |
| `creator` | `==` `!=` `~` | text |
| `parent` | `==` `!=` | an issue ID, or `""` for "no parent" |
| `label` | `==` `!=` `~` | `==` means the issue carries exactly that label |
| `text` | `~` | matches the ID, title **and** description together |
| `created` `updated` `closed` | `==` `!=` `<` `<=` `>` `>=` | `2026-01-01` or `2026-01-01T09:00:00Z` |
| `ready` `blocked` | — | used bare or negated: `ready`, `!blocked` |

Using an operator a field does not list is an error, not an empty result — `text == "x"`
and `status < "open"` both refuse.

## Values

- Quote a string: `assignee == "Ada Lovelace"`. Quotes are optional for a single word made
  of letters, digits and `_ : . / @ -`, so `type == bug` is fine.
- **Anything with a space must be quoted.** `text ~ drill nav` is a syntax error; write
  `text ~ "drill nav"`.
- A date is `YYYY-MM-DD` (midnight UTC) or a full `YYYY-MM-DDThh:mm:ssZ`. Both may be
  quoted or bare.

## Search

`taskmgr search` is the shorthand for text matching:

```bash
taskmgr search export         # same as: list -q 'text ~ "export"'
taskmgr search drill nav      # every word must match, in any order
```

Multiple words are AND-ed independently of order or adjacency. For an exact phrase, drop
back to the query form: `list -q 'text ~ "drill nav"'` matches only where the words are
adjacent and in that order.

`search` takes `--all`, `--sort`, `--reverse` and `--limit` like `list`, and a `-q`
expression is AND-ed with the text match.

## Four things that surprise people

- **`~` is a substring, not a word.** `text ~ "rate"` also matches "separate", and
  `assignee ~ "an"` matches "Hannah". `~` is always case-insensitive; `==` never is.
- **`blocked` is not `status == "blocked"`.** `blocked` and `ready` are computed from the
  dependency graph; the `blocked` *status* is a manual label nothing keeps in sync. See
  [Concepts](concepts.md#ready-and-blocked).
- **Closed issues are excluded by default.** Pass `--all`, or write an expression that
  asks for them: `status == "closed"` or any `closed` comparison brings them in.
  `status != "closed"` does **not** — it selects active work.
- **`label == "x"` is membership, not equality of the whole set.** It is true when the
  issue carries `x` among its labels. `label ~ "area:"` is how you match a family.

## Ordering and limits

Ordering is not part of the expression — it is a flag:

```bash
taskmgr list -q 'ready' --sort priority --limit 10
taskmgr list --sort created --reverse
```

`--sort` takes `work` (the default: priority, then oldest), `id`, `priority`, `created`,
`updated` or `closed`. Every sort breaks ties on the ID, so the order is stable between
runs.
