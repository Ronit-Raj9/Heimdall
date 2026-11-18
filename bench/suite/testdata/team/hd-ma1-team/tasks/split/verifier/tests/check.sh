test -f TEAM_RESULT.md && test -f worker-a.txt && test -f worker-b.txt && grep -q "worker-a: done" TEAM_RESULT.md && grep -q "worker-b: done" TEAM_RESULT.md
