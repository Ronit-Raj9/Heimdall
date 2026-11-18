#!/bin/sh
# Mutant control: trusts the 2021 election report. It cites real corpus pages, so its sources are
# not invented, but its answer is wrong. Admission requires it to fail.
printf '%s\n' 'Agnes Polgrain' > answer.txt
cat > sources.json <<'JSON'
[
  "https://coastwiki.example/wiki/Kestrel_Footbridge",
  "https://calder-gazette.example/2021/05/penhallow-elects-polgrain"
]
JSON
