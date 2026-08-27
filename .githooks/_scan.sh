#!/usr/bin/env bash
# Shared scanner for the partner-disclosure hooks.
#
# WHY THIS EXISTS: a design partner's name, their class namespaces, and a
# description of their CI topology once reached this repository in comments
# and a test fixture. No code of theirs was ever here, but the references
# alone were enough to be misread as "they built this from our source" —
# which cost real trust and took a full history rewrite to undo. The rule
# now is absolute: nothing identifying a partner enters this repo again,
# not in code, not in a fixture, not in a commit message.
#
# The forbidden names live in scripts/disclosure-patterns.local, which is
# GITIGNORED ON PURPOSE — writing them into a tracked file would recreate
# the exact reference these hooks exist to prevent.
#
# FAIL CLOSED. If the pattern file is missing we block the commit rather
# than wave it through: a guard that silently checks nothing is worse than
# no guard, because you stop looking.
set -uo pipefail

PATTERNS_FILE="scripts/disclosure-patterns.local"

partner_regex() {
  if [ ! -f "$PATTERNS_FILE" ]; then
    cat >&2 <<EOF
BLOCKED: $PATTERNS_FILE is missing.

  It holds the partner/customer names that must never be committed, one
  extended-regex per line, and it is gitignored so the names themselves
  never live in the repository. Without it this check is blind to exactly
  what it was written for, so commits are refused rather than allowed
  through unchecked.

  Recreate it (ask the repo owner for the contents), then retry.
EOF
    exit 1
  fi
  local re=""
  while IFS= read -r pat; do
    case "$pat" in ''|'#'*) continue ;; esac
    if [ -z "$re" ]; then re="$pat"; else re="$re|$pat"; fi
  done < "$PATTERNS_FILE"
  if [ -z "$re" ]; then
    echo "BLOCKED: $PATTERNS_FILE contains no patterns." >&2
    exit 1
  fi
  printf '%s' "$re"
}

# Artifact shapes that would carry a MAPPING of someone's source tree —
# coverage documents, collected test lists, VSTest result files. These are
# the collector's own output format: real ones from a partner's suite would
# enumerate their namespaces and file paths wholesale. The repo has never
# tracked one, so this blocks a new class of mistake without false alarms.
FORBIDDEN_ARTIFACTS='(^|/)(coverage\.json|collected\.txt)$|\.(trx|coverage)$'
