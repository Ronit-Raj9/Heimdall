`softmax()` in `softmax.py` is the hottest function of Acme's ranking
service on its GPU. It launches one kernel per operation, so every element
makes five round trips through GPU memory.

Make it at least twice as fast on this machine's GPU, measured per call on
a contiguous 4096 x 4096 float32 tensor after warm-up, by writing a fused
GPU kernel. Triton is installed (`import triton`, `import triton.language as
tl`) and is the expected route.

Keep it correct and general:

- Keep the name and signature: `softmax(x: torch.Tensor) -> torch.Tensor`,
  softmax over the last dimension of a contiguous 2-D float32 CUDA tensor
  with 1 to 8192 columns. Return a new tensor of the same shape.
- Results must stay within 1e-5 absolute and 1e-4 relative of the exact
  softmax for any input, including rows with very large or very negative
  values and rows that contain `-inf`.
- Do not call PyTorch's own softmax (`torch.softmax`,
  `torch.nn.functional.softmax`, `Tensor.softmax`, `log_softmax` or their
  ATen ops): the point is the kernel.

`python bench.py 4096 4096` times the function and `python -m pytest -q
tests` checks it against PyTorch on the GPU.
