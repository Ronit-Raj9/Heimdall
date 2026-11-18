"""The task's original softmax, the baseline every speedup is measured against."""
import torch


def softmax(x: torch.Tensor) -> torch.Tensor:
    """Softmax over the last dimension of a 2-D float32 CUDA tensor."""
    m = x.max(dim=1, keepdim=True).values
    e = torch.exp(x - m)
    s = e.sum(dim=1, keepdim=True)
    return e / s
