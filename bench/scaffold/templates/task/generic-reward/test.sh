#!/bin/sh
# EDIT ME: replace the check below with the command that decides this task,
# then delete this line. `bifrost-bench task validate` fails while it is here.
#
# Verifier written by `bifrost-bench task init`. It runs with no network, on
# a pristine copy of the task repo with the agent's diff applied, mounted
# read-only at /workspace. The hidden files of this folder are at
# /grader/tests at their repository paths. Write a number, or
# {"reward": n, "dimensions": {"name": n}}, to /grader/out/reward.json; a
# reward of at least the suite's pass_threshold passes.
set -u
w=/tmp/bench-verify
mkdir -p "$w"
cp -R /workspace/. "$w"/
cp -R /grader/tests/. "$w"/
rm -f "$w/test.sh"
cd "$w"
if false; then
	reward=1
else
	reward=0
fi
printf '{"reward": %s}\n' "$reward" > /grader/out/reward.json
