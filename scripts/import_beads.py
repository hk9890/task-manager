#!/usr/bin/env python3
"""Migrate a beads tracker into a task-manager (.tasks) store.

This is a thin *adapter*: it translates a beads JSONL export into task-manager
import envelopes and feeds each to `taskmgr import`, which validates the record
against the data model, mints its id, and writes it (the single writer). All
beads-specific knowledge lives here; taskmgr knows nothing about beads.

Unlike a create→update→close replay, `taskmgr import` writes each issue as a
complete end-state, so original timestamps, closed state, and comments are
preserved faithfully in one pass.

Usage
-----
    # export from beads, then import into ./.tasks (creating it):
    python3 scripts/import_beads.py --prefix at

    # from an existing export file into a chosen directory:
    bd export -o beads.jsonl
    python3 scripts/import_beads.py --from beads.jsonl --dir /path/to/project

    # preview the envelopes without writing:
    python3 scripts/import_beads.py --dry-run

Mapping
-------
* IDs are minted by taskmgr, not the adapter: each issue is imported with no
  ``id`` so the store allocates a fresh, collision-resistant token (beads ids
  like ``at-zib.1.1`` aren't valid task-manager ids, so they can't be reused
  verbatim). The original id is preserved as a ``beads:<id>`` label, and a
  ``source_id → new-id`` map is written to ``--map-out``. Issues import in
  dependency order, one at a time, so an edge can reference the freshly minted
  id of an already-imported parent/blocker.
* Timestamps (created/updated/closed) and comments (author + time) are imported
  verbatim — no provenance footer needed.
* Labels are slugified to fit the label grammar (spaces → ``-``); a label that
  cannot be salvaged is dropped with a warning.
* Statuses outside taskmgr's set map to ``open`` and the original is preserved
  as an ``imported-status:<s>`` label (taskmgr's set is open/in_progress/blocked/
  deferred/closed, so most beads statuses pass through unchanged).
* Edges: issues are imported in dependency order so ``parent``/``blocked_by``
  targets exist. A ``related`` edge (or, in a dependency cycle, a ``parent`` /
  ``blocked_by`` edge) whose target is imported later is dropped and counted —
  the issue itself is still imported (a cycle can't be represented anyway).
"""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from collections import deque

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

VALID_TYPES = {"task", "bug", "feature", "epic", "chore"}
TYPE_ALIASES = {"decision": "task", "enhancement": "feature", "feat": "feature", "adr": "task"}
VALID_STATUSES = {"open", "in_progress", "blocked", "deferred", "closed"}
LABEL_RE = re.compile(r"^[a-z0-9][a-z0-9:._/-]*$")

# Control-character sanitization: taskmgr rejects control chars (they force a
# double-quoted YAML scalar). Strip ANSI escapes then any remaining C0/DEL,
# keeping tab and newline; normalise CR to LF.
ANSI_RE = re.compile(r"\x1b\[[0-9;?]*[ -/]*[@-~]")
CTRL_RE = re.compile(r"[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]")


def eprint(*a):
    print(*a, file=sys.stderr)


def confirm(msg: str) -> bool:
    try:
        return input(f"{msg} [y/N] ").strip().lower() in ("y", "yes")
    except EOFError:
        return False


def clean_text(s) -> str:
    if not s:
        return ""
    s = ANSI_RE.sub("", s)
    s = s.replace("\r\n", "\n").replace("\r", "\n")
    return CTRL_RE.sub("", s)


def slugify_label(label) -> str | None:
    """Coerce a beads label into the task-manager label grammar, or None."""
    s = (label or "").strip().lower()
    s = s.replace(" ", "-").replace("_", "-")
    s = re.sub(r"[^a-z0-9:._/-]", "", s)
    s = re.sub(r"-{2,}", "-", s).strip("-")
    s = re.sub(r"^[^a-z0-9]+", "", s)  # must start with [a-z0-9]
    s = s[:64]
    return s if s and LABEL_RE.match(s) else None


def map_type(t) -> str:
    t = (t or "task").lower()
    if t in VALID_TYPES:
        return t
    return TYPE_ALIASES.get(t, "task")


def map_status(st):
    """Return (taskmgr_status, extra_labels). Non-standard statuses → open + label."""
    st = (st or "open").lower()
    if st in VALID_STATUSES:
        return st, []
    slug = slugify_label(st)
    return "open", ([f"imported-status:{slug}"] if slug else [])


def clamp_priority(p) -> int:
    try:
        n = int(p)
    except (TypeError, ValueError):
        n = 2
    return max(0, min(4, n))


def build_description(rec: dict) -> str:
    body = clean_text(rec.get("description")).rstrip()
    notes = clean_text(rec.get("notes")).strip()
    if notes:
        body = (body + "\n\n## Notes\n" + notes).strip()
    return body


def edges(rec: dict):
    """Return (parent_beads_id|None, [blocker_beads_ids], [related_beads_ids])."""
    parent, blockers, related = None, [], []
    for d in rec.get("dependencies") or []:
        dep = d.get("depends_on_id")
        if not dep:
            continue
        t = d.get("type")
        if t == "parent-child":
            parent = dep
        elif t == "blocks":
            blockers.append(dep)
        elif t in ("related", "relates-to"):
            related.append(dep)
    return parent, blockers, related


def load_records(path: str | None) -> list[dict]:
    if path:
        with open(path) as f:
            lines = f.read().splitlines()
    else:
        eprint("Running `bd export` ...")
        fd, tmp_path = tempfile.mkstemp(suffix=".jsonl")
        os.close(fd)
        try:
            subprocess.run(["bd", "export", "-o", tmp_path], check=True,
                           capture_output=True, text=True)
            with open(tmp_path) as f:
                lines = f.read().splitlines()
        finally:
            os.unlink(tmp_path)
    recs = []
    for ln in lines:
        ln = ln.strip()
        if not ln:
            continue
        o = json.loads(ln)
        if o.get("_type", "issue") == "issue" and o.get("title"):
            recs.append(o)
    return recs


def derive_prefix(args, target: str) -> str:
    """A valid store prefix (^[a-z][a-z0-9]*$) from --prefix or the dir name."""
    if args.prefix:
        return args.prefix
    base = os.path.basename(os.path.abspath(target)).lower()
    p = re.sub(r"[^a-z0-9]", "", base)
    if not p or not p[0].isalpha():
        p = "at" + p
    return p[:10]


def topo_order(records: list[dict], idset: set[str]) -> list[dict]:
    """Order records so parent/blocked_by targets precede dependents (Kahn)."""
    ids = [r["id"] for r in records]
    rec_by_id = {r["id"]: r for r in records}
    indeg = {i: 0 for i in ids}
    adj: dict[str, list[str]] = {i: [] for i in ids}
    for r in records:
        parent, blockers, _ = edges(r)
        deps = {d for d in ([parent] + blockers) if d and d in idset}
        for d in deps:
            adj[d].append(r["id"])
            indeg[r["id"]] += 1
    q = deque(i for i in ids if indeg[i] == 0)
    order: list[str] = []
    while q:
        n = q.popleft()
        order.append(n)
        for m in adj[n]:
            indeg[m] -= 1
            if indeg[m] == 0:
                q.append(m)
    if len(order) != len(ids):  # cycle: append the rest; import rejects cyclic edges
        seen = set(order)
        order += [i for i in ids if i not in seen]
    return [rec_by_id[i] for i in order]


class Stats:
    def __init__(self):
        self.dropped_labels = 0
        self.skipped_related = 0
        self.dangling_edges = 0
        self.cyclic_edges = 0


def build_envelope(rec, idmap, idset, stats: Stats) -> dict:
    """Translate one beads record into a taskmgr import envelope.

    No ``id`` is set — taskmgr mints one. ``idmap`` maps a beads source id to the
    taskmgr id it was *already* minted with (issues imported so far, in dependency
    order); ``idset`` is every beads id in the export, used to tell a forward /
    cyclic edge (target imported later) apart from a dangling one (target not in
    the export). Both are dropped (keeping the issue) and counted.
    """
    status, extra_labels = map_status(rec.get("status"))

    labels: list[str] = []
    for l in rec.get("labels") or []:
        s = slugify_label(l)
        if s:
            if s not in labels:
                labels.append(s)
        else:
            stats.dropped_labels += 1
            eprint(f"  ! dropping unsalvageable label {l!r} on {rec['id']}")
    prov = slugify_label(f"beads:{rec['id']}")
    if prov and prov not in labels:
        labels.append(prov)
    for el in extra_labels:
        if el not in labels:
            labels.append(el)

    env = {
        # No "id": taskmgr mints a fresh, collision-resistant token. The original
        # beads id rides along in the beads:<id> label set above and as source_id.
        "source_id": rec["id"],
        "title": clean_text(rec["title"])[:200] or "(untitled)",
        "type": map_type(rec.get("issue_type")),
        "priority": clamp_priority(rec.get("priority")),
        "status": status,
        "assignee": clean_text(rec.get("owner"))[:128],
        "creator": clean_text(rec.get("created_by"))[:128],
        "labels": labels,
        "description": build_description(rec),
        "created_at": rec.get("created_at"),
        "updated_at": rec.get("updated_at") or rec.get("created_at"),
    }

    # Edges reference the taskmgr id of an already-imported issue. idmap holds
    # source -> minted id for issues imported so far; dependency order guarantees
    # a parent/blocker precedes its dependents, so it is already in idmap. A
    # target still absent is a forward/cyclic edge when it lives elsewhere in the
    # export (idset) — taskmgr can't represent a cyclic parent/blocked_by anyway —
    # or dangling when it doesn't; both are dropped (keeping the issue) and counted.
    parent, blockers, related = edges(rec)
    if parent:
        if parent in idmap:
            env["parent"] = idmap[parent]
        elif parent in idset:
            stats.cyclic_edges += 1
            eprint(f"  ! {rec['id']}: parent {parent} is part of a cycle — edge dropped")
        else:
            stats.dangling_edges += 1
            eprint(f"  ! {rec['id']}: parent {parent} not in export — skipped")
    bl = []
    for b in blockers:
        if b in idmap:
            bl.append(idmap[b])
        elif b in idset:
            stats.cyclic_edges += 1
            eprint(f"  ! {rec['id']}: blocker {b} is part of a cycle — edge dropped")
        else:
            stats.dangling_edges += 1
            eprint(f"  ! {rec['id']}: blocker {b} not in export — skipped")
    if bl:
        env["blocked_by"] = bl
    rel = []
    for r in related:
        if r in idmap:
            rel.append(idmap[r])
        elif r in idset:
            stats.skipped_related += 1  # target imported later / cycle
        else:
            stats.dangling_edges += 1
    if rel:
        env["related"] = rel

    if status == "closed":
        env["closed_at"] = rec.get("closed_at") or rec.get("updated_at") or rec.get("created_at")
        reason = clean_text(rec.get("close_reason"))
        if reason:
            env["close_reason"] = reason

    comments = []
    for c in rec.get("comments") or []:
        text = clean_text(c.get("text")).strip()
        if not text:
            continue
        comments.append({
            "author": clean_text(c.get("author"))[:128],
            "created_at": c.get("created_at") or rec.get("created_at"),
            "body": text,
        })
    if comments:
        env["comments"] = comments
    return env


def main() -> int:
    ap = argparse.ArgumentParser(description="Migrate beads issues into a .tasks store via `taskmgr import`.")
    ap.add_argument("--from", dest="src", help="beads JSONL export (default: run `bd export`)")
    ap.add_argument("--dir", default=".", help="target project dir holding .tasks (default: cwd)")
    ap.add_argument("--taskmgr", default=os.path.join(REPO_ROOT, "bin", "taskmgr"),
                    help="path to the taskmgr binary (default: <repo>/bin/taskmgr)")
    ap.add_argument("--prefix", help="ID prefix for the new store (default: derived from dir name)")
    ap.add_argument("--yes", "-y", action="store_true",
                    help="overwrite an existing .tasks store without prompting")
    ap.add_argument("--map-out", default=os.path.join(REPO_ROOT, "scripts", ".beads-import-map.json"))
    ap.add_argument("--dry-run", action="store_true", help="print envelopes without writing")
    args = ap.parse_args()

    if not args.dry_run and not (os.path.isfile(args.taskmgr) and os.access(args.taskmgr, os.X_OK)):
        eprint(f"taskmgr not found at {args.taskmgr}. Build it first:  mise run build")
        return 1

    records = load_records(args.src)
    eprint(f"Loaded {len(records)} beads issues.")
    if not records:
        return 0

    prefix = derive_prefix(args, args.dir)
    idset = {r["id"] for r in records}
    ordered = topo_order(records, idset)
    stats = Stats()

    if args.dry_run:
        # Preview only. taskmgr mints each id at import time; model that with an
        # identity map so edges render as their source ids and the forward/cyclic/
        # dangling counts match a real run (which assumes every record imports).
        idmap: dict[str, str] = {}
        envelopes = []
        for rec in ordered:
            envelopes.append(build_envelope(rec, idmap, idset, stats))
            idmap[rec["id"]] = rec["id"]
        eprint(f"(dry-run) {len(envelopes)} envelopes; "
               f"{stats.dropped_labels} labels dropped, {stats.skipped_related} related skipped, "
               f"{stats.dangling_edges} dangling edges")
        print(json.dumps(envelopes[0], indent=2) if envelopes else "[]")
        return 0

    # A fresh store is required: taskmgr mints new ids, so importing into a
    # populated store would duplicate every issue. If one exists, ask before wiping.
    tasks_dir = os.path.join(args.dir, ".tasks")
    if os.path.isdir(tasks_dir):
        abspath = os.path.abspath(tasks_dir)
        if args.yes or confirm(f"A .tasks store already exists at {abspath}.\nDelete it and re-import?"):
            shutil.rmtree(tasks_dir)
            eprint(f"Removed existing store at {abspath}.")
        else:
            eprint("Keeping the existing store — import aborted.")
            return 0

    init = subprocess.run([args.taskmgr, "-C", args.dir, "init", "--prefix", prefix],
                          capture_output=True, text=True)
    if init.returncode != 0:
        eprint(f"taskmgr init failed: {init.stderr.strip()}")
        return 1

    # Import one issue at a time, in dependency order, letting taskmgr mint each
    # id. The minted id comes back in the {source_id, id} result and goes into
    # idmap so a later issue's parent/blocked_by/related can reference it. A single
    # --batch call can't do this: the whole stream is written before taskmgr runs,
    # so earlier-minted ids aren't known when the later edges are built.
    idmap: dict[str, str] = {}
    ok: list[str] = []
    failed: list[str] = []
    for rec in ordered:
        env = build_envelope(rec, idmap, idset, stats)
        proc = subprocess.run([args.taskmgr, "-C", args.dir, "--json", "import"],
                              input=json.dumps(env), capture_output=True, text=True)
        minted = None
        if proc.returncode == 0:
            try:
                minted = json.loads(proc.stdout)["id"]
            except (json.JSONDecodeError, KeyError):
                minted = None
        if not minted:
            err = proc.stderr.strip() or proc.stdout.strip() or f"exit {proc.returncode}"
            failed.append(rec["id"])
            eprint(f"  ! import failed for {rec['id']}: {err}")
            continue
        idmap[rec["id"]] = minted
        ok.append(rec["id"])

    with open(args.map_out, "w") as f:
        json.dump(idmap, f, indent=2, sort_keys=True)

    eprint(f"\nImported {len(ok)}/{len(records)} issues into {tasks_dir}.")
    eprint(f"  labels dropped: {stats.dropped_labels}, related skipped: {stats.skipped_related}, "
           f"cyclic edges dropped: {stats.cyclic_edges}, dangling edges: {stats.dangling_edges}, "
           f"failed records: {len(failed)}")
    eprint(f"Wrote source_id -> new-id map to {args.map_out}")
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
