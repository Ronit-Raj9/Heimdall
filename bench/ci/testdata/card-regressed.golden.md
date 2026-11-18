<!-- heimdall-verdict -->
### Heimdall verdict: REGRESSED (hold)

Compared: agent files at `main` vs `HEAD`

> 3 of 10 tasks have unreviewed instructions (drafted by `tasks mine`; review them with `heimdall task review`).

| | A: main (1a2b3c) | B: HEAD \| candidate |
|---|---|---|
| verified success (clean) | 28/30 (93%) | 19/30 (63%), 1 unknown |
| cost per verified success | $0.0412 | unknown (usage missing) |

**Effect** of B against A on verified_success (clean): -30.0 pp, interval [-52.0 pp, -8.0 pp], sign-flip p = 0.031 over 10 tasks (0 better, 4 worse, 6 tied)

**Policy** enterprise-default: block (1 of 2 checks not passing)  
**Integrity**: clean

**Tasks that differ most**

| task | A | B | B - A |
|---|---|---|---|
| `m-1` | 1.00 | 0.00 | -1.00 |
| `m-4` | 0.67 | 0.00 | -0.67 |
| `m-3` | 1.00 | 0.67 | -0.33 |

Experiment `e_compare-golden`, analysis `a_golden1`; [verdict page](http://127.0.0.1:8090/verdict?exp=e_compare-golden); evidence: .heimdall/runs/e_compare-golden
