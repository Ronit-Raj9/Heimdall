"""The classifier Acme's support desk routes tickets with (fictional).

MLP(hidden, depth) maps a 16-number ticket embedding to one of 10 queues.
hidden is 16 to 1024 units, depth 1 to 4 hidden layers.
"""
import torch.nn as nn

FEATURES, CLASSES = 16, 10
MAX_HIDDEN, MAX_DEPTH = 1024, 4


class MLP(nn.Module):
    def __init__(self, hidden=128, depth=1):
        super().__init__()
        if not (16 <= hidden <= MAX_HIDDEN and 1 <= depth <= MAX_DEPTH):
            raise ValueError(f"hidden must be 16..{MAX_HIDDEN} and depth 1..{MAX_DEPTH}")
        layers = [nn.Linear(FEATURES, hidden), nn.ReLU()]
        for _ in range(depth - 1):
            layers += [nn.Linear(hidden, hidden), nn.ReLU()]
        layers.append(nn.Linear(hidden, CLASSES))
        self.net = nn.Sequential(*layers)

    def forward(self, x):
        return self.net(x)
