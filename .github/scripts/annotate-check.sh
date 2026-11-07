#!/usr/bin/env bash
# Turn the log of a failed `dagger call check` into GitHub annotations and a job summary.
# Usage: annotate-check.sh <log>   (run from the repository root)
set -euo pipefail

# The CLI ends with "Error: <check's error>", which lists each failed step and its output.
report=$(sed 's/\x1b\[[0-9;]*m//g' "$1" | sed -n '/^Error: /,$p' | sed '1s/^Error: //; s/ \[traceparent:[^]]*\]$//')
[ -n "$report" ] || exit 0

escape() { local s=${1//%/%25}; s=${s//$'\r'/%0D}; printf '%s' "${s//$'\n'/%0A}"; }

# One annotation per failed step, e.g. "lint: exit code 1:"
grep -E '^(lint|test|vuln|helm|docs|build): ' <<<"$report" | while IFS= read -r line; do
  echo "::error title=Check: ${line%%:*} failed::$(escape "$line")"
done || true

# Line annotations for "file.go:line[:col]: message" from lint, vet, the compiler and tests
grep -oE '[A-Za-z0-9_./-]+\.go:[0-9]+(:[0-9]+)?: .+' <<<"$report" | sort -u | while IFS= read -r m; do
  [[ $m =~ ^([^:]+):([0-9]+)(:([0-9]+))?:\ (.*)$ ]] || continue
  file=${BASH_REMATCH[1]} line=${BASH_REMATCH[2]} col=${BASH_REMATCH[4]} msg=${BASH_REMATCH[5]}
  # shortcut: go test prints only the file name; take the first match, ambiguous if two packages share it
  [ -f "$file" ] || file=$(git ls-files "*/$file" | head -n1)
  [ -n "$file" ] || continue
  echo "::error file=$file,line=$line${col:+,col=$col}::$(escape "$msg")"
done || true

{
  echo '### Check failed'
  echo '```'
  echo "$report"
  echo '```'
} >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
