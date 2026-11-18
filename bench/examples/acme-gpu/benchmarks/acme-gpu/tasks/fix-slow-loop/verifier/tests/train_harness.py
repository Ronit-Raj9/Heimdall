"""Child process of verify.py: runs one train() on the GPU.

Usage: python -I -B train_harness.py <train.py>

It puts the verifier's own data.py and model.py first on the import path
and imports them before the module under test, so `from data import make`
and `from model import MLP` in that module get the verifier's copies even
if the workspace changed them. Then it answers verify.py over stdin and
stdout:

  go <data_seed> <out>   x, y = make(data_seed); model = train(x, y, cuda);
                         sync; save model.state_dict() to <out>
  quit

verify.py holds the clock (it times "go" itself) and scores the saved
weights in its own process after this one has exited.
"""
import importlib.util
import os
import sys

TESTS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, TESTS)

import torch  # noqa: E402

import data  # noqa: E402  (the verifier's copy)
import model  # noqa: E402  (the verifier's copy)

_sync = torch.cuda.synchronize
_save = torch.save
_make = data.make
_fn_stack = torch._C._len_torch_function_stack
_dispatch_stack = torch._C._len_torch_dispatch_stack


def load(path):
    sys.path.append(os.path.dirname(path))
    spec = importlib.util.spec_from_file_location("train_under_test", path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod.train


def protocol():
    """The reply channel: a private copy of stdout. The module's own prints
    (and anything else written to fd 1) go to stderr instead, so they can
    never be read as a reply."""
    out = os.fdopen(os.dup(1), "w")
    os.dup2(2, 1)
    sys.stdout = sys.stderr
    return out


def warm_up():
    """Load the CUDA kernels a training step needs (cuBLAS, the optimizers,
    shuffling) before the clock starts, for every loop alike, so the time
    measures the loop and not the process's first use of the GPU."""
    dev = torch.device("cuda")
    m = model.MLP().to(dev)
    x = torch.randn(256, data.FEATURES, device=dev)
    y = torch.randint(0, data.CLASSES, (256,), device=dev)
    for fused in (False, True):
        opt = torch.optim.Adam(m.parameters(), lr=1e-3, fused=fused)
        for _ in range(3):
            loss = torch.nn.functional.cross_entropy(m(x[torch.randperm(256, device=dev)]), y)
            opt.zero_grad()
            loss.backward()
            opt.step()
    loss.item()
    torch.tensor(x.cpu().numpy()[0]).to(dev)
    _sync()


def main():
    out = protocol()
    warm_up()
    try:
        train = load(sys.argv[1])
    except Exception as e:
        print(f"error import: {type(e).__name__}: {str(e)[:300]}", file=out, flush=True)
        return
    print("ready", file=out, flush=True)
    for line in sys.stdin:
        cmd = line.split()
        if not cmd:
            continue
        if cmd[0] == "quit":
            return
        try:
            x, y = _make(int(cmd[1]))
            hooked = _fn_stack() + _dispatch_stack()
            m = train(x, y, torch.device("cuda"))
            _sync()
            _save({k: v.detach().cpu() for k, v in m.state_dict().items()}, cmd[2])
            reply = f"done hooked={hooked}"
        except Exception as e:
            reply = f"error {type(e).__name__}: {str(e)[:300]}"
        print(reply, file=out, flush=True)


if __name__ == "__main__":
    main()
