"""The task's original training loop: the baseline every speedup is measured against."""
import argparse
import time

import numpy as np
import torch
import torch.nn.functional as F
from torch.utils.data import DataLoader, Dataset

from data import make
from model import MLP

EPOCHS, BATCH, LR, SEED = 10, 256, 1e-3, 0


class Points(Dataset):
    def __init__(self, x, y):
        self.x, self.y = x, y

    def __len__(self):
        return len(self.x)

    def __getitem__(self, i):
        return torch.tensor(self.x[i]), torch.tensor(self.y[i])


def train(x, y, device):
    torch.manual_seed(SEED)
    loader = DataLoader(Points(x, y), batch_size=BATCH, shuffle=True)
    model = MLP().to(device)
    opt = torch.optim.Adam(model.parameters(), lr=LR)
    history = []
    for epoch in range(EPOCHS):
        for xb, yb in loader:
            xb, yb = xb.to(device), yb.to(device)
            loss = F.cross_entropy(model(xb), yb)
            opt.zero_grad()
            loss.backward()
            opt.step()
            torch.cuda.synchronize()
            history.append(loss.item())
            acc = (model(xb).argmax(1).cpu().numpy() == yb.cpu().numpy()).mean()
        print(f"epoch {epoch + 1} loss {np.mean(history[-len(loader):]):.4f} last batch acc {acc:.3f}", flush=True)
    return model


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data-seed", type=int, default=11)
    ap.add_argument("--out", default="model.pt")
    args = ap.parse_args()
    x, y = make(args.data_seed)
    start = time.perf_counter()
    model = train(x, y, torch.device("cuda"))
    torch.cuda.synchronize()
    print(f"trained in {time.perf_counter() - start:.2f} s")
    torch.save(model.state_dict(), args.out)


if __name__ == "__main__":
    main()
