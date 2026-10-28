#!/usr/bin/env bash
# PostToolUse: format any .go file Claude just edited or wrote.
f=$(jq -r '.tool_input.file_path // empty')
case "$f" in
  *.go) gofmt -w "$f" ;;
esac
exit 0
