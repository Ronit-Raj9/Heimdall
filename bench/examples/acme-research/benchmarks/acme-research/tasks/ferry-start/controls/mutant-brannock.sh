#!/bin/sh
# Mutant control: takes the Brannock ferry's start year. It cites real corpus pages, so its sources are
# not invented, but its answer is wrong. Admission requires it to fail.
printf '%s\n' '1958' > answer.txt
cat > sources.json <<'JSON'
[
  "https://coastwiki.example/wiki/Brannock_ferry"
]
JSON
