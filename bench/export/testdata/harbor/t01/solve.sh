#!/bin/bash
# Reference solution exported from Heimdall (solution/patch.diff).
set -euo pipefail
SOL="$(cd "$(dirname "$0")" && pwd)"
cd /workspace
if command -v git >/dev/null 2>&1; then
	git -c safe.directory='*' apply --whitespace=nowarn "$SOL/patch.diff"
else
	patch -p1 < "$SOL/patch.diff"
fi
