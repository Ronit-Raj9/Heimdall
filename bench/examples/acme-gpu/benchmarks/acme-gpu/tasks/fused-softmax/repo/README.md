# ranking-kernels

GPU helpers of Acme's ranking service (fictional).

- `softmax.py`: `softmax(x)`, row-wise softmax of a 2-D float32 CUDA tensor.
- `bench.py`: times it, `python bench.py 4096 4096`.
- `tests/`: `python -m pytest -q tests` (needs the GPU).

The container has PyTorch 2.15 (CUDA 13.2) and Triton 3.9; no network.
