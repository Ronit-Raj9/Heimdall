#!/bin/sh
# Mutant control: trusts the 2022 wiki fee. It cites real corpus pages, so its sources are
# not invented, but its answer is wrong. Admission requires it to fail.
printf '%s\n' '18' > answer.txt
cat > sources.json <<'JSON'
[
  "https://coastwiki.example/wiki/Skerry_Harbour"
]
JSON
