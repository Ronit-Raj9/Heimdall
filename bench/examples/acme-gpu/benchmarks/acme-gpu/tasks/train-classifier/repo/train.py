"""First attempt at the ticket router: python train.py

Trains model.MLP() on train.npz and writes model.pt and model.json. It
reaches about 83% on new tickets; the desk needs better.
"""
import json

import numpy as np
import torch
import torch.nn.functional as F

from model import MLP

HIDDEN, DEPTH, EPOCHS, BATCH, LR = 128, 1, 5, 256, 1e-3


def main():
    torch.manual_seed(0)
    data = np.load("train.npz")
    x = torch.from_numpy(data["x"].astype(np.float32)).cuda()
    y = torch.from_numpy(data["y"].astype(np.int64)).cuda()
    split = int(0.9 * len(x))
    model = MLP(HIDDEN, DEPTH).cuda()
    opt = torch.optim.Adam(model.parameters(), lr=LR)
    for epoch in range(EPOCHS):
        perm = torch.randperm(split, device="cuda")
        for i in range(0, split, BATCH):
            idx = perm[i:i + BATCH]
            loss = F.cross_entropy(model(x[idx]), y[idx])
            opt.zero_grad()
            loss.backward()
            opt.step()
        with torch.no_grad():
            acc = (model(x[split:]).argmax(1) == y[split:]).float().mean().item()
        print(f"epoch {epoch + 1} validation accuracy {acc:.4f}")
    torch.save(model.state_dict(), "model.pt")
    with open("model.json", "w") as f:
        json.dump({"hidden": HIDDEN, "depth": DEPTH}, f)


if __name__ == "__main__":
    main()
