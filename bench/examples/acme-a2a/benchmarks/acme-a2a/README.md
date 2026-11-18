# acme-a2a

Three support questions for agents that speak A2A, part of the
`examples/acme-a2a` project (docs/a2a-agents.md). Each task asks one
question about the fictional Acme Tea handbook in the workspace
(`tasks/<task>/repo/docs/handbook.md`). The answer is matched against the
task's hidden `verifier/answer.yaml` by the answer_match grader, with no
model.

| Task | Question | Match |
|---|---|---|
| `refund-window` | the refund window for an unopened tin, in days | numeric 30 |
| `dispatch-cutoff` | the working-day dispatch cut-off hour | numeric 14 |
| `team-brief` | the warehouse city and the courier | exact "leeds parcelwing", case, punctuation and spacing ignored |

`controls/` holds what admission requires to fail: the empty control, an
exploit (lists every candidate value) and plausible wrong answers.

Probes (layer `a2a`, for streaming a2a arms only): `stream-drop` closes the
launcher's side of the stream after the first event, `stream-delay` stops
reading it for 3 s.
