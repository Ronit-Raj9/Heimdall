# fused-softmax: what the verifier checks

| Dimension | Holds | Instruction line |
|---|---|---|
| `correct` | 1 when all 10 cases (1x1 up to 128x8192 and 5x8191, values scaled by 1e4, shifted by -1e4, rows with `-inf`) and one randomly chosen timed call per round match the float64 softmax within 1e-5 absolute and 1e-4 relative | "Results must stay within 1e-5 absolute and 1e-4 relative of the exact softmax for any input" |
| `allowed` | 0 when the module called PyTorch's own softmax or log_softmax (guarded in the child process) | "Do not call PyTorch's own softmax" |
| `speedup`, `speedup_lo`, `speedup_hi` | median per-round ratio of the original's time over the agent's, and its distribution-free interval (2nd smallest and 2nd largest of 9 rounds, about 96%) | "at least twice as fast ... per call on a contiguous 4096 x 4096 float32 tensor after warm-up" |
| `ref_ms`, `agent_ms` | median milliseconds per call of each | |

Reward 1 when `correct` is 1 and `speedup_lo` is at least 2.0.

Seeds come from `os.urandom` at verification time, so no input exists
before the verifier makes it, and the agent's code runs only in a child
process whose answers this process checks: the clock and the reference stay
in the verifier.
