#!/bin/bash
# Verifier exported from Heimdall suite coding-core@1.0.0, task t01@1.0.0 (grader hidden_tests).
# Runs inside the agent's container after the agent (Harbor's shared verifier);
# README.md says how this differs from the bench's own grade.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
G=/tmp/hd-grader
WS=/workspace
mkdir -p /logs/verifier
command -v python3 >/dev/null 2>&1 || { echo "python3 is needed to read the verifier result" >&2; exit 3; }
rm -rf "$G" && mkdir -p "$G/tests" "$G/out" || exit 3
cp -a "$HERE/heimdall/hidden/." "$G/tests/" || exit 3
# Paths the agent may not change are reset from the pristine repository
# (the suite's verifier.restore list), as the bench's clean room does.
for p in tests conftest.py pytest.ini pyproject.toml setup.cfg tox.ini sitecustomize.py; do
	rm -rf "$WS/$p"
	if [ -e "$HERE/heimdall/restore/$p" ]; then mkdir -p "$(dirname "$WS/$p")" && cp -a "$HERE/heimdall/restore/$p" "$WS/$p"; fi
done
cd "$WS" || exit 3
if command -v timeout >/dev/null 2>&1; then T="timeout -k 10 300"; else T=""; fi
$T python -I -B -m pytest -q -p no:cacheprovider --rootdir=/tmp/hd-grader --junitxml=/tmp/hd-grader/out/junit.xml /tmp/hd-grader/tests
code=$?
python3 "$HERE/heimdall/check.py" junit /tmp/hd-grader/out/junit.xml "$code" "$HERE/heimdall/required.txt" > "$G/reward" || exit 3
cp "$G/reward" /logs/verifier/reward.txt
