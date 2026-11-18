#!/bin/sh
# Reference control: train the router the way a careful agent would, a
# wider and deeper MLP for 40 epochs with AdamW and a one-cycle learning
# rate, on the GPU, and leave model.pt and model.json. Admission runs it in
# the agent's place and requires the verifier to pass it three times in a
# row.
exec python3 -I - <<'PY'
import json
import sys

import numpy as np
import torch
import torch.nn.functional as F

sys.path.insert(0, ".")
from model import MLP

HIDDEN, DEPTH, EPOCHS, BATCH = 512, 3, 40, 256
torch.manual_seed(0)
data = np.load("train.npz")
x = torch.from_numpy(data["x"].astype(np.float32)).cuda()
y = torch.from_numpy(data["y"].astype(np.int64)).cuda()
model = MLP(HIDDEN, DEPTH).cuda()
opt = torch.optim.AdamW(model.parameters(), lr=2e-3, weight_decay=1e-4, fused=True)
steps = EPOCHS * ((len(x) + BATCH - 1) // BATCH)
sched = torch.optim.lr_scheduler.OneCycleLR(opt, max_lr=3e-3, total_steps=steps)
for epoch in range(EPOCHS):
    perm = torch.randperm(len(x), device="cuda")
    for i in range(0, len(x), BATCH):
        idx = perm[i:i + BATCH]
        loss = F.cross_entropy(model(x[idx]), y[idx])
        opt.zero_grad(set_to_none=True)
        loss.backward()
        opt.step()
        sched.step()
torch.save(model.state_dict(), "model.pt")
with open("model.json", "w") as f:
    json.dump({"hidden": HIDDEN, "depth": DEPTH}, f)
print("trained", HIDDEN, DEPTH, EPOCHS)
PY
