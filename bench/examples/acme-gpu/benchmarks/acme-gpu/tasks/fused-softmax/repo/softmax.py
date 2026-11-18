"""Row-wise softmax for the attention scores of Acme's ranking model.

The ranking service calls softmax() on every batch of scores, and the
profiler puts it at the top of the GPU time. It runs one kernel per
operation (max, subtract, exp, sum, divide), so every element is read and
written to GPU memory five times.
"""
import torch


def softmax(x: torch.Tensor) -> torch.Tensor:
    """Softmax over the last dimension of a 2-D float32 CUDA tensor."""
    m = x.max(dim=1, keepdim=True).values
    e = torch.exp(x - m)
    s = e.sum(dim=1, keepdim=True)
    return e / s
