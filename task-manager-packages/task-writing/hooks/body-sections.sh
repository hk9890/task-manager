#!/bin/sh
# body-sections.sh — refuse an issue whose description skips a required section.
#
# HOOK-SPEC: the payload {event, old, new} arrives on stdin; the exit code is the
# decision (0 allow, non-zero deny) and stdout carries the reason. The check is
# structural — that a heading is present, and that the criteria list has at least
# one item. Whether the criteria are any good is not something a gate can see.
#
# It gates the body, so it only looks at a write that changes the body. `new` is
# the whole candidate issue and not a delta, so re-reading it on every edit meant
# an issue written before this package was installed could not have its priority
# or its labels touched again — the store went half-usable the moment the gate
# arrived. When `old` carries the same description as `new`, this write is not
# about the body and the gate stands aside.
#
# It fails OPEN when it cannot parse the payload at all: with no JSON reader on
# the machine the alternative is refusing every write in the store, which is a
# worse outcome than an unchecked body. An allow may carry a hint, and that is
# what says the check did not run.
#
# Usage: body-sections.sh [--epic]
set -eu

case "${1:-}" in
--epic) SECTIONS="## Context|## Outcome|## Success criteria" ; NEEDS_CHECKLIST=0 ;;
*) SECTIONS="## Context|## Problem|## Recommended action|## Acceptance criteria" ; NEEDS_CHECKLIST=1 ;;
esac

payload=$(cat)

if command -v python3 >/dev/null 2>&1; then
  READER=python3
elif command -v jq >/dev/null 2>&1; then
  READER=jq
else
  echo "task-writing: no python3 or jq on this machine, so the body was not checked"
  exit 0
fi

# The reader prints "skip", or "check" followed by the body on the next line. One
# invocation decides and extracts, so the gate spawns one process while the store
# lock is held rather than two.
if [ "$READER" = jq ]; then
  read_payload() {
    printf '%s' "$payload" | jq -r '
      if .old != null and ((.old.description // "") == (.new.description // ""))
      then "skip"
      else "check\n" + (.new.description // "")
      end'
  }
else
  read_payload() {
    printf '%s' "$payload" | python3 -c '
import json,sys
try:
    p = json.load(sys.stdin)
except Exception:
    sys.exit(3)
old = p.get("old")
new = p.get("new") or {}
body = new.get("description") or ""
if old is not None and (old.get("description") or "") == body:
    sys.stdout.write("skip")
else:
    sys.stdout.write("check\n" + body)
'
  }
fi

# The guard is on both readers. jq exits non-zero on a payload it cannot parse,
# and under `set -e` that ended the script with jq's own status and an empty
# stdout — which taskmgr reads as a denial with no reason, the exact opposite of
# the fail-open this header promises.
read_out=$(read_payload) || {
  echo "task-writing: the hook payload could not be parsed, so the body was not checked"
  exit 0
}

case $read_out in
skip) exit 0 ;;
esac

nl='
'
body=${read_out#check}
body=${body#$nl}

missing=""
old_ifs=$IFS
IFS='|'
for section in $SECTIONS; do
  case "
$body" in
  *"
$section"*) ;;
  *) missing="$missing $section" ;;
  esac
done
IFS=$old_ifs

if [ -n "$missing" ]; then
  echo "task-writing: the description is missing:$missing"
  echo "Run 'taskmgr guide pkg:task-writing:bodies' for what each section owns."
  exit 1
fi

if [ "$NEEDS_CHECKLIST" -eq 1 ]; then
  criteria=${body#*## Acceptance criteria}
  # Both cases of the ticked box: GFM accepts "- [x]" and "- [X]", and the
  # capital is what an author marking every criterion done tends to write.
  case "$criteria" in
  *"- [ ]"*) ;;
  *"- [x]"*) ;;
  *"- [X]"*) ;;
  *)
    echo "task-writing: '## Acceptance criteria' has no checklist item."
    echo "Every criterion is a command to run or an observation to make, written so"
    echo "someone who was not there can execute it: '- [ ] npm test -- auth passes'."
    exit 1
    ;;
  esac
fi

exit 0
