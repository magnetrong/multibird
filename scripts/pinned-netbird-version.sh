#!/usr/bin/env bash
# Print the pinned netbird version (no leading v), for CI to install the matching binary.
set -euo pipefail
cd "$(dirname "$0")/.."
grep -E '^\s*github.com/netbirdio/netbird v' go.mod | awk '{print $2}' | sed 's/^v//'
