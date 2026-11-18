The nightly job that retrains Acme's churn classifier no longer fits in its
window: `train()` in `train.py` is far slower than the GPU should allow.

Make `train(x, y, device)` at least 2.5 times faster on this machine's GPU
without losing accuracy:

- Keep what it computes: `model.MLP()` as defined in `model.py`, Adam with
  learning rate 1e-3, 10 epochs over the data in shuffled batches of 256,
  `torch.manual_seed(0)` first. Return the trained `MLP`.
- The model it returns must classify fresh points from the same data as
  well as the current loop's model does (within about one percentage point).
- Keep the signature, and leave `data.py` and `model.py` as they are; the
  time that counts is the call to `train()` itself, from the arrays `make()`
  returns to the returned model, with the GPU synchronized.

`python train.py --data-seed 11` prints the training time.
