# Hand-written fixture in KernelBench's problem format (not upstream data).
import torch
import torch.nn as nn


class Model(nn.Module):
    """A 2-D convolution."""

    def __init__(self, in_channels: int, out_channels: int, kernel_size: int):
        super(Model, self).__init__()
        self.conv = nn.Conv2d(in_channels, out_channels, kernel_size)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.conv(x)


batch_size = 16
in_channels = 3
out_channels = 64
kernel_size = 3
height = width = 256


def get_inputs():
    x = torch.randn(batch_size, in_channels, height, width)
    return [x]


def get_init_inputs():
    return [in_channels, out_channels, kernel_size]
