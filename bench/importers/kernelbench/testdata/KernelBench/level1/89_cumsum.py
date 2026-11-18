# Hand-written fixture in KernelBench's problem format (not upstream data).
import torch
import torch.nn as nn


class Model(nn.Module):
    def __init__(self, dim):
        super(Model, self).__init__()
        self.dim = dim

    def forward(self, x):
        return torch.cumsum(x, dim=self.dim)


batch_size = 128
input_shape = (4000,)
dim = 1


def get_inputs():
    return [torch.rand(batch_size, *input_shape)]


def get_init_inputs():
    return [dim]
