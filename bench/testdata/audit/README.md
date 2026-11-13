# Audit fixtures

Inputs for `verdict.ParseAuditCSV` and `verdict.Audit`. Each CSV has the header
`task,a_pass,a_n,b_pass,b_n`: arm A passed `a_pass` of `a_n` attempts on the
task, arm B passed `b_pass` of `b_n`.

## maxim-tb21.csv

Maxim's published Terminal-Bench 2.1 model-routing result, rebuilt from its
counts in the README of `maximhq/bifrost-benchmarking`,
`model-routing-benchmark/terminal-bench`:

| | |
|---|---|
| Tasks | 77, one attempt each |
| Arm A | fixed Opus, 74 of 77 |
| Arm B | router, 73 of 77 |
| Only A passed | 4 tasks |
| Only B passed | 3 tasks |
| Both passed | 70 tasks |

The per-task identities are not reproduced. The task ids `tb-01` to `tb-77`
are placeholders, and which placeholder carries which discordant result is
arbitrary (`tb-01` to `tb-04` only A, `tb-05` to `tb-07` only B). The sign-flip
test depends only on the per-task differences, so the placement does not
change the result: 7 discordant tasks split 4 to 3 give p = 1.0 and an
inconclusive verdict.
