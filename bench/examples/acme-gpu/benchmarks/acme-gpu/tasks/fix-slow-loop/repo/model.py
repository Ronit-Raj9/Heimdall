import torch.nn as nn

from data import CLASSES, FEATURES


class MLP(nn.Module):
    def __init__(self, hidden=256, depth=2):
        super().__init__()
        layers = [nn.Linear(FEATURES, hidden), nn.ReLU()]
        for _ in range(depth - 1):
            layers += [nn.Linear(hidden, hidden), nn.ReLU()]
        layers.append(nn.Linear(hidden, CLASSES))
        self.net = nn.Sequential(*layers)

    def forward(self, x):
        return self.net(x)
