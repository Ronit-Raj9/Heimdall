# train-classifier: what the verifier checks

| Dimension | Holds | Instruction line |
|---|---|---|
| `loads` | 1 when `model.json` holds exactly the integers `hidden` (16 to 1024) and `depth` (1 to 4) and `model.pt` is a float32 state dict that loads strictly into the verifier's own `MLP(hidden, depth)` | "`model.pt`: the `state_dict()` of a `model.MLP(hidden, depth)`", "`model.json`: `{"hidden": <int>, "depth": <int>}`" |
| `heldout_score` | accuracy on 10,000 held-out tickets from `gen.py` (the hidden source of `train.npz`), drawn with a seed the agent's container never has | "at least 90.5%", "scored on new tickets from the same source as `train.npz`" |
| `target`, `params` | the 0.905 target and the model's parameter count | |

Reward 1 when `loads` is 1 and `heldout_score` is at least 0.905. No code
the agent wrote runs in the verifier: the weights are loaded with
`torch.load(weights_only=True)`.
