Acme's support desk routes tickets with the classifier in `model.py`,
trained on the 20,000 labelled tickets in `train.npz`. `train.py` is a first
attempt and gets about 83% of new tickets right; the desk needs at least
90.5%.

Train a better model on this machine's GPU and leave two files in the
workspace:

- `model.pt`: the `state_dict()` of a `model.MLP(hidden, depth)`
  (`torch.save(model.state_dict(), "model.pt")`), with float32 weights.
- `model.json`: `{"hidden": <int>, "depth": <int>}`, the arguments that
  build that model. `hidden` is 16 to 1024, `depth` 1 to 4.

It is scored on new tickets from the same source as `train.npz`, which are
not in the workspace. Keep `model.py` as it is: the model must load into
that class. You have the GPU for the whole session, and the session's time
limit is the GPU time you have.
