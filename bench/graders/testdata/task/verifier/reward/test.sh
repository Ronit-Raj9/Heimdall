#!/bin/sh
# Harbor-style verifier, run as PID 1 of the verifier container. The scoring
# step imports the agent's code, so it runs as a child process. This script
# then kills every process the child left behind and writes the reward file
# last, so a stray write from agent code cannot stand in for the result.
passed=$(python -I -B /grader/tests/score.py | tail -n 1)
kill -9 -1 2>/dev/null
case "$passed" in
0 | 1 | 2 | 3 | 4) ;;
*) passed=0 ;;
esac
exec python -I -B -c '
import json
import sys

passed = int(sys.argv[1])
reward = passed / 4
with open("/grader/out/reward.json", "w") as f:
    json.dump({"reward": reward, "dimensions": {"correctness": reward, "cases_passed": passed}}, f)
' "$passed"
