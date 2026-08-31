#!/usr/bin/env bash
# CI gate: the netbird Go module pin in go.mod and TestedMax in
# internal/version/versions.go must move together (see CLAUDE.md "Version policy").
set -euo pipefail
cd "$(dirname "$0")/.."

gomod=$(grep -E '^\s*github.com/netbirdio/netbird v' go.mod | awk '{print $2}' | sed 's/^v//')
tested=$(grep -E 'TestedMax\s*=' internal/version/versions.go | sed -E 's/.*"([^"]+)".*/\1/')

if [[ -z "$gomod" || -z "$tested" ]]; then
  echo "FAIL: could not extract versions (go.mod: '$gomod', versions.go: '$tested')" >&2
  exit 1
fi
if [[ "$gomod" != "$tested" ]]; then
  echo "FAIL: go.mod pins netbird v$gomod but TestedMax is $tested." >&2
  echo "Bump both together — see CLAUDE.md 'Version policy'." >&2
  exit 1
fi
echo "OK: netbird pin v$gomod == TestedMax $tested"
