# ticket-router

Acme's support desk sends every ticket to one of 10 queues (fictional).

- `train.npz`: 20,000 labelled tickets, `x` (16 numbers each, float16) and
  `y` (queue 0 to 9).
- `model.py`: the classifier, `MLP(hidden, depth)`.
- `train.py`: a first training script; writes `model.pt` (the state dict)
  and `model.json` (`{"hidden": ..., "depth": ...}`).

The container has PyTorch 2.15 (CUDA 13.2) and the GPU; no network.
