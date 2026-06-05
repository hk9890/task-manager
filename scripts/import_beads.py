#!/usr/bin/env python3
"""Import beads issues into an agent-tasks (.tasks) store via atctl.

One-off migration: reads a beads JSONL export and recreates every issue in a
target .tasks store — fields, labels, parent / blocked-by edges, comments, and
closed state — driving the validated `atctl` CLI (the single writer).

Usage
-----
    # export from beads, then import into ./.tasks (creating it):
    python3 scripts/import_beads.py --init --prefix at

    # from an existing export file into a chosen directory:
    bd export -o beads.jsonl
    python3 scripts/import_beads.py --from beads.jsonl --dir /path/to/project

    # preview without writing:
    python3 scripts/import_beads.py --dry-run

Notes
-----
* agent-tasks IDs are reallocated (beads ids like ``at-zib.1.1`` are not valid
  agent-tasks ids). A ``beads-id -> new-id`` map is printed at the end and
  written to ``--map-out`` (default ``scripts/.beads-import-map.json``).
* File timestamps (created/updated/closed) are set at import time. The original
  beads timestamps are preserved in a footer appended to each issue body, so no
  provenance is lost.
* Edges and comments are applied while everything is still open; closed issues
  are closed last.
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

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

VALID_TYPES = {"task", "bug", "feature", "epic", "chore"}
TYPE_ALIASES = {"decision": "task", "enhancement": "feature", "feat": "feature", "adr": "task"}
VALID_STATUSES = {"open", "in_progress", "blocked", "closed"}
LABEL_RE = re.compile(r"^[a-z0-9][a-z0-9:._/-]*$")


def eprint(*a):
    print(*a, file=sys.stderr)


def confirm(msg: str) -> bool:
    """Ask a yes/no question on the terminal; default No (also on a closed stdin)."""
    try:
        return input(f"{msg} [y/N] ").strip().lower() in ("y", "yes")
    except EOFError:
        return False


def sanitize_label(label: str) -> str | None:
    s = label.strip().lower()
    return s if LABEL_RE.match(s) else None


def map_type(t: str | None) -> str:
    t = (t or "task").lower()
    if t in VALID_TYPES:
        return t
    return TYPE_ALIASES.get(t, "task")


def clamp_priority(p) -> str:
    try:
        n = int(p)
    except (TypeError, ValueError):
        n = 2
    return str(max(0, min(4, n)))


def body_with_footer(rec: dict) -> str:
    body = (rec.get("description") or "").rstrip()
    notes = (rec.get("notes") or "").strip()
    if notes:
        body = (body + "\n\n## Notes\n" + notes).strip()
    bits = [f"created {rec.get('created_at', '?')}", f"updated {rec.get('updated_at', '?')}"]
    if rec.get("closed_at"):
        bits.append(f"closed {rec['closed_at']}")
    footer = f"*Imported from beads `{rec['id']}` — " + ", ".join(bits) + ".*"
    return (body + "\n\n---\n" + footer + "\n").lstrip("\n")


class Atctl:
    def __init__(self, binary: str, target: str, dry_run: bool):
        self.binary = binary
        self.target = target
        self.dry_run = dry_run

    def _run(self, args: list[str], stdin: str | None = None, capture_json: bool = False):
        cmd = [self.binary, "-C", self.target]
        if capture_json:
            cmd.append("--json")
        cmd += args
        if self.dry_run:
            shown = " ".join(a if " " not in a else repr(a) for a in args)
            eprint(f"  DRY: atctl {shown}" + (" <stdin>" if stdin is not None else ""))
            return {"id": f"dry-{args}"} if capture_json else ""
        proc = subprocess.run(cmd, input=stdin, capture_output=True, text=True)
        if proc.returncode != 0:
            raise RuntimeError(f"atctl {' '.join(args)} failed: {proc.stderr.strip()}")
        out = proc.stdout.strip()
        return json.loads(out) if capture_json and out else out

    def init(self, prefix: str | None):
        args = ["init"]
        if prefix:
            args += ["--prefix", prefix]
        self._run(args)

    def create(self, *, title, typ, priority, assignee, labels, body) -> str:
        args = ["create", "--title", title, "--type", typ, "--priority", priority,
                "--description-file", "-"]
        if assignee:
            args += ["--assignee", assignee[:128]]
        for l in labels:
            args += ["--label", l]
        res = self._run(args, stdin=body, capture_json=True)
        return res["id"]

    def set_parent(self, new_id, new_parent):
        self._run(["update", new_id, "--parent", new_parent])

    def add_dep(self, dependent, blocker):
        self._run(["dep", "add", dependent, blocker])

    def add_comment(self, new_id, author, text):
        args = ["comment", "add", new_id, "--file", "-"]
        if author:
            args += ["--author", author[:128]]
        self._run(args, stdin=text)

    def set_status(self, new_id, status):
        self._run(["update", new_id, "--status", status])

    def close(self, new_id, reason):
        args = ["close", new_id]
        if reason:
            args += ["--reason", reason]
        self._run(args)


def load_records(path: str | None) -> list[dict]:
    if path:
        with open(path) as f:
            lines = f.read().splitlines()
    else:
        eprint("Running `bd export` ...")
        # bd writes only to a real file (`-o -` makes a file literally named "-"),
        # so always export to a temp file and read it back.
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


def main() -> int:
    ap = argparse.ArgumentParser(description="Import beads issues into a .tasks store via atctl.")
    ap.add_argument("--from", dest="src", help="beads JSONL export (default: run `bd export`)")
    ap.add_argument("--dir", default=".", help="target project dir holding .tasks (default: cwd)")
    ap.add_argument("--atctl", default=os.path.join(REPO_ROOT, "bin", "atctl"),
                    help="path to the atctl binary (default: <repo>/bin/atctl)")
    ap.add_argument("--prefix", help="ID prefix for the new store (atctl derives one if omitted)")
    ap.add_argument("--yes", "-y", action="store_true",
                    help="overwrite an existing .tasks store without prompting")
    ap.add_argument("--map-out", default=os.path.join(REPO_ROOT, "scripts", ".beads-import-map.json"))
    ap.add_argument("--dry-run", action="store_true", help="print actions without writing")
    args = ap.parse_args()

    if not args.dry_run and not (os.path.isfile(args.atctl) and os.access(args.atctl, os.X_OK)):
        eprint(f"atctl not found at {args.atctl}. Build it first:  mise run build")
        return 1

    records = load_records(args.src)
    eprint(f"Loaded {len(records)} beads issues.")
    if not records:
        return 0

    at = Atctl(args.atctl, args.dir, args.dry_run)

    # A fresh store is required (import is additive — re-importing into an existing
    # store would duplicate). If one already exists, ask before wiping it.
    tasks_dir = os.path.join(args.dir, ".tasks")
    if os.path.isdir(tasks_dir):
        abspath = os.path.abspath(tasks_dir)
        if args.dry_run:
            eprint(f"(dry-run) existing store at {abspath} would be deleted and re-imported")
        elif args.yes or confirm(f"A .tasks store already exists at {abspath}.\nDelete it and re-import?"):
            shutil.rmtree(tasks_dir)
            eprint(f"Removed existing store at {abspath}.")
        else:
            eprint("Keeping the existing store — import aborted.")
            return 0
    at.init(args.prefix)

    # Pass 1 — create every issue (open), build the id map.
    idmap: dict[str, str] = {}
    for rec in records:
        labels = []
        for l in rec.get("labels") or []:
            s = sanitize_label(l)
            if s and s not in labels:
                labels.append(s)
            elif not s:
                eprint(f"  ! dropping invalid label {l!r} on {rec['id']}")
        new_id = at.create(
            title=rec["title"][:200],
            typ=map_type(rec.get("issue_type")),
            priority=clamp_priority(rec.get("priority")),
            assignee=rec.get("owner") or "",
            labels=labels,
            body=body_with_footer(rec),
        )
        idmap[rec["id"]] = new_id
        eprint(f"  + {rec['id']} -> {new_id}  {rec['title'][:48]}")

    # Pass 2 — edges + comments (everything still open, so refs resolve).
    for rec in records:
        new_id = idmap[rec["id"]]
        parent, blockers, related = edges(rec)
        if parent and parent in idmap:
            at.set_parent(new_id, idmap[parent])
        elif parent:
            eprint(f"  ! {rec['id']}: parent {parent} not in export — skipped")
        for b in blockers:
            if b in idmap:
                at.add_dep(new_id, idmap[b])
            else:
                eprint(f"  ! {rec['id']}: blocker {b} not in export — skipped")
        if related:
            eprint(f"  ! {rec['id']}: 'related' edges can't be set post-create via atctl — skipped {related}")
        for c in rec.get("comments") or []:
            text = (c.get("text") or "").strip()
            if text:
                at.add_comment(new_id, c.get("author") or "", text)

    # Pass 3 — apply non-open status (close moves to closed/).
    for rec in records:
        new_id = idmap[rec["id"]]
        st = rec.get("status")
        if st == "closed":
            at.close(new_id, rec.get("close_reason") or "")
        elif st in ("in_progress", "blocked"):
            at.set_status(new_id, st)

    if not args.dry_run:
        with open(args.map_out, "w") as f:
            json.dump(idmap, f, indent=2, sort_keys=True)
        eprint(f"\nWrote id map -> {args.map_out}")
    eprint(f"Imported {len(idmap)} issues into {os.path.join(args.dir, '.tasks')}.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
