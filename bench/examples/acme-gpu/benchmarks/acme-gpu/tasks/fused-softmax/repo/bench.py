"""Time softmax() on the GPU: python bench.py [rows] [cols]."""
import sys

import torch

from softmax import softmax


def main():
    rows = int(sys.argv[1]) if len(sys.argv) > 1 else 4096
    cols = int(sys.argv[2]) if len(sys.argv) > 2 else 4096
    x = torch.randn(rows, cols, device="cuda")
    for _ in range(10):
        softmax(x)
    torch.cuda.synchronize()
    start, end = torch.cuda.Event(enable_timing=True), torch.cuda.Event(enable_timing=True)
    start.record()
    for _ in range(100):
        softmax(x)
    end.record()
    torch.cuda.synchronize()
    print(f"softmax {rows}x{cols}: {start.elapsed_time(end) / 100:.4f} ms per call")


if __name__ == "__main__":
    main()
