#!/bin/sh
# Mutant control: reports the 2023 annual total. It cites real corpus pages, so its sources are
# not invented, but its answer is wrong. Admission requires it to fail.
printf '%s\n' '412,000' > answer.txt
cat > sources.json <<'JSON'
[
  "https://calder-gazette.example/2024/01/brightwater-record-year"
]
JSON
