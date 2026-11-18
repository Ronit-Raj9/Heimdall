"""Hidden verifier of train-classifier. Runs in the clean room on the run's GPU.

It reads the two artifacts the agent left, copied out of the workspace by
the bench: /grader/artifacts/model/model.pt and
/grader/artifacts/config/model.json. No code of the agent runs here: the
weights are loaded with torch.load(weights_only=True), which accepts tensors
and plain containers only, into the verifier's own copy of model.MLP. The
held-out tickets come from gen.py, which only this folder holds, with a seed
the agent's container never sees.

Reward 1 when the model loads and its held-out accuracy is at least TARGET.
Dimensions: loads, heldout_score, target, params.
"""
import json
import os
import sys

TESTS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, TESTS)

import torch  # noqa: E402

import gen  # noqa: E402
from model import MAX_DEPTH, MAX_HIDDEN, MLP  # noqa: E402

ART = "/grader/artifacts"
OUT = "/grader/out/reward.json"
TARGET = 0.905


def write(reward, dims, note):
    with open(OUT, "w") as f:
        json.dump({"reward": reward, "dimensions": {k: float(v) for k, v in dims.items()}}, f)
    print(note, json.dumps(dims), flush=True)


def main():
    dims = {"loads": 0, "target": TARGET}
    try:
        with open(os.path.join(ART, "config", "model.json")) as f:
            cfg = json.load(f)
        hidden, depth = cfg["hidden"], cfg["depth"]
        if type(hidden) is not int or type(depth) is not int or set(cfg) != {"hidden", "depth"}:
            raise ValueError("model.json must hold exactly the integers hidden and depth")
        if not (16 <= hidden <= MAX_HIDDEN and 1 <= depth <= MAX_DEPTH):
            raise ValueError(f"hidden {hidden} or depth {depth} out of range")
        model = MLP(hidden, depth)
        state = torch.load(os.path.join(ART, "model", "model.pt"), map_location="cpu", weights_only=True)
        if not isinstance(state, dict) or any(not isinstance(v, torch.Tensor) or v.dtype != torch.float32 for v in state.values()):
            raise ValueError("model.pt must be a state dict of float32 tensors")
        model.load_state_dict(state, strict=True)
    except Exception as e:
        return write(0, dims, f"the model does not load: {type(e).__name__}: {str(e)[:300]}")
    dims["loads"] = 1
    dims["params"] = sum(p.numel() for p in model.parameters())
    x, y = gen.sample(gen.HELDOUT_SEED, gen.HELDOUT)
    model = model.cuda().eval()
    with torch.no_grad():
        pred = model(torch.from_numpy(x).cuda()).argmax(1).cpu()
    acc = float((pred == torch.from_numpy(y)).float().mean())
    dims["heldout_score"] = acc
    reward = 1 if acc >= TARGET else 0
    write(reward, dims, "passed" if reward else f"held-out accuracy {acc:.4f} is below {TARGET}")


if __name__ == "__main__":
    main()
