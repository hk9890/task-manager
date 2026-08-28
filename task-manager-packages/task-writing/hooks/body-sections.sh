#!/bin/sh
# body-sections.sh — refuse an issue whose description skips a required section.
#
# HOOK-SPEC: the payload {event, old, new} arrives on stdin; the exit code is the
# decision (0 allow, non-zero deny) and stdout carries the reason. The check is
# structural — that a heading is present, and that the criteria list has at least
# one item. Whether the criteria are any good is not something a gate can see.
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

if [ "$READER" = jq ]; then
  body=$(printf '%s' "$payload" | jq -r '.new.description // ""')
else
  body=$(printf '%s' "$payload" | python3 -c '
import json,sys
try:
    p = json.load(sys.stdin)
except Exception:
    sys.exit(3)
new = p.get("new") or {}
sys.stdout.write(new.get("description") or "")
') || {
    echo "task-writing: the hook payload could not be parsed, so the body was not checked"
    exit 0
  }
fi

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
  case "$criteria" in
  *"- [ ]"*) ;;
  *"- [x]"*) ;;
  *)
    echo "task-writing: '## Acceptance criteria' has no checklist item."
    echo "Every criterion is a command to run or an observation to make, written so"
    echo "someone who was not there can execute it: '- [ ] npm test -- auth passes'."
    exit 1
    ;;
  esac
fi

exit 0
