# fix-slow-loop: what the verifier checks

| Dimension | Holds | Instruction line |
|---|---|---|
| `accuracy_kept` | 1 when, in every round, the agent's model scores at most 1.5 points below the original loop's model on 10,000 held-out points of that round's data | "must classify fresh points from the same data as well as the current loop's model does (within about one percentage point)" |
| `heldout_score`, `heldout_ref` | median held-out accuracy of the agent's models and of the original's | |
| `speedup`, `speedup_lo`, `speedup_hi` | median, smallest and largest per-round ratio of the original loop's seconds over the agent's | "at least 2.5 times faster on this machine's GPU" |
| `time_to_target_s` | median seconds of the agent's `train()` call, reported when accuracy was kept | "the time that counts is the call to `train()` itself" |
| `ref_s` | median seconds of the original loop | |

Reward 1 when `accuracy_kept` is 1 and `speedup` is at least 2.5.

Each of the 3 rounds draws a fresh data seed from `os.urandom`, so a model
trained in advance cannot match the round's data; the verifier's own
`data.py` and `model.py` are used whatever the workspace holds; the weights
are read the moment the child says it is done; the held-out points are drawn
only after every child process is gone.
