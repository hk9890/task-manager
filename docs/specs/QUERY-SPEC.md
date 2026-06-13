# Query Specification — filter expressions

This document specifies the **filter-expression language** used to select issues:
its grammar, the fields and operators, precedence, value syntax, and evaluation and
error semantics.

The language is an **engine-level** concern. The SDK owns parsing and evaluation
(`Store.Query` / `Filter.Expr`, see [SDK-SPEC.md](SDK-SPEC.md)); the `taskmgr` CLI is
a thin pass-through that forwards its `-q/--query` string to the engine unchanged
(see [CLI-SPEC.md](CLI-SPEC.md)). Both front ends therefore share one grammar — the
one defined here — and there is exactly one implementation of it.

The design target is a small, SQL-flavoured predicate language that an agent or a
human can write from memory. It is deliberately **not** a general query engine:
no joins, no projections, no ordering, no aggregation (ordering and limits are
presentation concerns handled by the caller, not the expression).

---

## 1. Grammar

Whitespace between tokens is insignificant. The expression is parsed to a boolean
predicate that is evaluated once per candidate issue.

```ebnf
expr       = or_expr ;
or_expr    = and_expr , { "||" , and_expr } ;
and_expr   = unary    , { "&&" , unary } ;
unary      = [ "!" ] , primary ;
primary    = "(" , expr , ")" | predicate ;

predicate  = comparison | bool_field ;
comparison = field , op , value ;
op         = "==" | "!=" | "<" | "<=" | ">" | ">=" | "~" ;

bool_field = "ready" | "blocked" ;
field      = "status" | "type" | "priority" | "assignee" | "creator" | "parent"
           | "label"  | "text" | "created"  | "updated"  | "closed" ;

value      = number | string | bareword ;
number     = digit , { digit } ;
string     = '"' , { char_no_dquote | escape } , '"' ;
escape     = "\" , ( '"' | "\" ) ;
bareword   = word_char , { word_char } ;   (* word_char = [A-Za-z0-9_:./@-] *)
```

- **Precedence**, tightest to loosest: `!`  →  `&&`  →  `||`. Parentheses override.
  All binary operators are left-associative. `a || b && c` parses as
  `a || (b && c)`; `!ready && blocked` parses as `(!ready) && blocked`.
- **The empty expression** (absent, empty, or whitespace-only) is the always-true
  predicate — it selects every issue in scope. This is what `taskmgr list` with no
  `-q` uses.

---

## 2. Fields, value types, and operators

Each field has a value type that fixes which operators are legal. Applying an
operator a field does not support is a parse error (§4).

| Field | Type | Operators | Value syntax |
|---|---|---|---|
| `status` | enum | `==` `!=` | `open` / `in_progress` / `blocked` / `deferred` / `closed` |
| `type` | enum | `==` `!=` | `task` / `bug` / `feature` / `epic` / `chore` |
| `priority` | int | `==` `!=` `<` `<=` `>` `>=` | integer (`0`–`4` stored) |
| `assignee` | string | `==` `!=` `~` | quoted or bareword |
| `creator` | string | `==` `!=` `~` | quoted or bareword |
| `parent` | issue ID | `==` `!=` | an issue ID, e.g. `"dtt-0007"`; `==` may be `""` (no parent) |
| `label` | string set | `==` `!=` `~` | quoted or bareword |
| `text` | virtual string | `~` | quoted or bareword |
| `created` | date | `==` `!=` `<` `<=` `>` `>=` | ISO date or timestamp (§3) |
| `updated` | date | `==` `!=` `<` `<=` `>` `>=` | ISO date or timestamp (§3) |
| `closed` | date | `==` `!=` `<` `<=` `>` `>=` | ISO date or timestamp (§3) |
| `ready` | bool | — (bare) | used bare or negated: `ready`, `!ready` |
| `blocked` | bool | — (bare) | used bare or negated: `blocked`, `!blocked` |

**Per-field matching semantics:**

- **enum / `priority` / `parent`** — `==` / `!=` compare the field value directly.
  `priority` also supports the ordering operators (numeric). Any non-negative integer
  is a legal `priority` literal; only `0`–`4` are storable, so an out-of-range bound
  simply matches all or none (`priority < 5` matches every issue, `priority == 7`
  matches none) instead of erroring.
- **`assignee`** — `==` / `!=` exact; `~` case-insensitive substring.
- **`creator`** — same as `assignee`: `==` / `!=` exact; `~` case-insensitive substring.
- **`label`** — the issue carries a *set* of labels. `label == "x"` is true iff the
  set contains exactly `"x"` (membership); `label != "x"` is its negation;
  `label ~ "x"` is true iff some label contains the case-insensitive substring `x`.
- **`text`** — a virtual field: the case-insensitive concatenation of the issue's
  `id`, `title`, and description body. Only `~` (substring) is defined.
- **`created` / `updated` / `closed`** — chronological comparison (§3). On an issue
  with no `closed` timestamp, every `closed` comparison is false. Likewise, on
  an issue with no `created` or `updated` value (absent or zero), every
  `created` / `updated` comparison is false — a missing timestamp has no value
  that can satisfy any ordering bound.
- **`ready` / `blocked`** — computed predicates with the meanings fixed by the
  storage spec (TASK-STORAGE-SPEC.md §9): `ready` = open with no open blocker;
  `blocked` = non-closed with ≥1 open blocker. These are derived from the
  dependency graph and are **independent of the `status` field**: the `blocked`
  predicate is **not** the same as `status == "blocked"`. The `blocked` *status*
  is a manual label the engine never sets or clears automatically — an issue can
  carry `status == "blocked"` with no open blocker (so not `blocked` here), or be
  `blocked` here while its status is `open`/`in_progress`.

**String comparison & case:** field names and enum tokens are lowercase. `==` / `!=`
on strings are exact and case-sensitive; `~` is always case-insensitive.

**Multi-word values must be quoted.** Barewords exclude spaces (§3), so a substring
search spanning a space has to be quoted: `text ~ "drill nav"`, not `text ~ drill
nav` (the latter is a syntax error — `nav` is a trailing token).

---

## 3. Values

- **Numbers** are bare decimal integers (`priority` only).
- **Strings** are either a **quoted** string (`"..."`, supporting `\"` and `\\`
  escapes) or a **bareword** matching `[A-Za-z0-9_:./@-]+`. Quoting is required when
  the value is empty or contains any character outside the bareword set (notably
  spaces). `assignee == "Ada Lovelace"` needs quotes; `type == bug` does not.
- **Dates** use the storage timestamp form (TASK-STORAGE-SPEC.md §6): a full
  `YYYY-MM-DDThh:mm:ssZ`, or a date-only `YYYY-MM-DD` which is interpreted as
  midnight UTC (`T00:00:00Z`). Comparison is on the resulting instant, so
  `closed > "2026-01-01"` means *closed strictly after 2026-01-01T00:00:00Z*. Either
  form may be quoted or bare (an ISO timestamp is a valid bareword).

---

## 4. Errors

Parsing rejects, and the caller surfaces the failure (SDK: a typed error; CLI: exit
code 1 with a message), on:

- an **unknown field** or an unknown bare predicate;
- an **operator not permitted** for the field (e.g. `status < "open"`, `text == "x"`);
- a **malformed value** for the field's type (`priority` not a non-negative integer;
  an unparseable date; an unknown enum token);
- a **syntax error**: unbalanced parentheses, a dangling operator, a missing value,
  or trailing tokens after a complete expression.

A well-formed expression that simply matches nothing is **not** an error — it
returns an empty result.

**Expression nesting depth:** parenthesised sub-expressions may be nested to a
maximum depth of **256 levels**. Input that exceeds this limit is rejected with
a parse error ("expression nesting too deep") rather than crashing. In practice
no real query approaches this limit.

---

## 5. Evaluation scope (not part of the grammar)

The expression decides *whether* an issue matches; it does **not** decide which
partitions are scanned. By default only the hot (active) set is evaluated.

**Cold-scope predicate (normative).** The cold (`closed/`) partition is scanned iff
*any* of these holds:

1. the caller opts in — `taskmgr --all`, `Filter.IncludeClosed`, or
   `FindOptions.IncludeClosed`;
2. the parsed expression contains a `status == "closed"` atom (positive equality
   only); or
3. the parsed expression contains any `closed`-field comparison (any operator).

Nothing else auto-scans cold — in particular `status != "closed"` does **not** (it
selects active work); to include closed issues under such a query the caller must opt
in explicitly. The predicate is computed from the **parsed expression**, so there is
exactly one detector: `Criteria` / `Find` derive their scope by building the
expression and running this same predicate, never by inspecting the struct, so a
`Criteria` and its hand-written equivalent always scope identically. This rule lives
with the caller (CLI-SPEC.md §3, SDK-SPEC.md §3–4), not with the grammar, so the same
expression is portable across front ends.

---

## 6. Examples

```text
status == "open"
status == "open" && priority <= 1
type == bug && label ~ "area:db"
ready && priority <= 2
text ~ "drill" && !blocked
assignee == "hans" && (type == bug || type == chore)
creator == "ada" && status == "open"
closed > "2026-01-01"
parent == "dtt-0007"
```

---

## 7. Stability & extension

The fields and operators in §2 are the current contract. Adding a new field, a new
operator, or a new bare predicate is an **additive** change (an expression valid
today stays valid). Removing or repurposing one is breaking and is versioned with
the SDK module. The grammar is intentionally frozen at this shape — selection only;
no ordering, projection, or aggregation is ever added here (those stay caller-side).

**Structured construction.** Callers that build expressions from typed inputs should
use the SDK's `Criteria.Build` (SDK-SPEC.md §3) rather than concatenating strings:
it is the canonical producer of this syntax and the single owner of value
quoting/escaping and precedence. It only *produces* an expression — evaluation stays
in the one engine. Ordering, limit, offset, and the total match count are
presentation concerns handled caller-side (`Filter` / `Page`), never part of this
grammar.
