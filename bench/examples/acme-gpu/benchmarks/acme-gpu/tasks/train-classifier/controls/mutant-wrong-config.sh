#!/bin/sh
# Mutant: a trained model whose model.json names another size (hidden 512
# for a 256-unit model), so the weights do not load into MLP(hidden, depth);
# it must fail.
exec python3 -I - <<'PY'
import json
import sys

import numpy as np
import torch
import torch.nn.functional as F

sys.path.insert(0, ".")
from model import MLP

torch.manual_seed(0)
data = np.load("train.npz")
x = torch.from_numpy(data["x"].astype(np.float32)).cuda()
y = torch.from_numpy(data["y"].astype(np.int64)).cuda()
model = MLP(256, 2).cuda()
opt = torch.optim.Adam(model.parameters(), lr=1e-3)
for epoch in range(10):
    perm = torch.randperm(len(x), device="cuda")
    for i in range(0, len(x), 256):
        idx = perm[i:i + 256]
        loss = F.cross_entropy(model(x[idx]), y[idx])
        opt.zero_grad()
        loss.backward()
        opt.step()
torch.save(model.state_dict(), "model.pt")
with open("model.json", "w") as f:
    json.dump({"hidden": 512, "depth": 2}, f)
PY
