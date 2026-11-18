#!/bin/sh
# Mutant control: trusts a forum guess. It cites real corpus pages, so its sources are
# not invented, but its answer is wrong. Admission requires it to fail.
printf '%s\n' '1986-08-14' > answer.txt
cat > sources.json <<'JSON'
[
  "https://harbourtalk.example/threads/first-lantern-tide"
]
JSON
