#!/bin/sh
# Mutant control: uses the 2014 census figure. It cites real corpus pages, so its sources are
# not invented, but its answer is wrong. Admission requires it to fail.
printf '%s\n' '44,950' > answer.txt
cat > sources.json <<'JSON'
[
  "https://coastwiki.example/wiki/Ashcombe"
]
JSON
