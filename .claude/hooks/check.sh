#!/usr/bin/env bash
# Stop: before Claude finishes, require go vet + go test to pass.
# On failure, exit 2 sends the output back to Claude so it fixes the problem.
INPUT=$(cat)

# Don't loop forever: if this hook already blocked once, let Claude stop.
if [ "$(echo "$INPUT" | jq -r '.stop_hook_active')" = "true" ]; then
  exit 0
fi

cd "$CLAUDE_PROJECT_DIR" || exit 0

# Nothing to check until there is Go code.
[ -n "$(find . -name '*.go' -not -path './.git/*' -print -quit)" ] || exit 0

if ! out=$( { go vet ./... && go test ./... ; } 2>&1 ); then
  echo "go vet / go test failed. Fix these before finishing:" >&2
  echo "$out" | tail -n 60 >&2
  exit 2
fi
exit 0
