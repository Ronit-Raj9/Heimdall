#!/bin/sh
# Mutant control: takes the road bridge's span. It cites real corpus pages, so its sources are
# not invented, but its answer is wrong. Admission requires it to fail.
printf '%s\n' '340' > answer.txt
cat > sources.json <<'JSON'
[
  "https://coastwiki.example/wiki/Ferrow_Road_Bridge"
]
JSON
