"""Child process of verify.py: runs one softmax module on the GPU.

Usage: python -I -B harness.py <module.py>

It imports torch first and keeps its own references to every function it
uses, installs a guard that refuses PyTorch's own softmax, and only then
imports the module under test, so a module that patches torch afterwards
does not change what this harness calls. It then answers verify.py over
stdin and stdout, one line per command:

  check <case> <seed>    run one correctness case, save the output to /tmp
  prep <seed> <calls>    make <calls> fresh inputs from <seed>
  go                     call softmax() once on each prepared input, then sync
  dump <k>               save the output of call k to /tmp
  quit

verify.py holds the clock (it times "go" itself) and checks the saved
outputs against its own float64 reference, so this process never reports
a time or a verdict that is trusted.
"""
import importlib.util
import os
import sys

import torch

_randn = torch.randn
_sync = torch.cuda.synchronize
_save = torch.save
_Generator = torch.Generator
_fn_stack = torch._C._len_torch_function_stack
_dispatch_stack = torch._C._len_torch_dispatch_stack

FORBIDDEN = []


def _guard(owner, name):
    orig = getattr(owner, name, None)
    if orig is None:
        return

    def refused(*args, **kwargs):
        FORBIDDEN.append(f"{getattr(owner, '__name__', owner)}.{name}")
        raise RuntimeError(f"{name} is not allowed in this task")

    setattr(owner, name, refused)


def install_guard():
    """Refuse PyTorch's own softmax in this process, before the module under
    test is imported. Only the child installs it, never verify.py."""
    for owner, name in [(torch, "softmax"), (torch, "log_softmax"), (torch, "_softmax"), (torch, "_log_softmax"),
                        (torch.nn.functional, "softmax"), (torch.nn.functional, "log_softmax"),
                        (torch.Tensor, "softmax"), (torch.Tensor, "log_softmax"),
                        (torch.special, "softmax"), (torch.special, "log_softmax")]:
        _guard(owner, name)
    for name in ["_softmax", "softmax", "_log_softmax", "log_softmax", "_safe_softmax"]:
        try:
            getattr(torch.ops.aten, name)
        except (AttributeError, RuntimeError):
            continue
        _guard(torch.ops.aten, name)


def cases():
    """Correctness cases: (shape, scale, -inf every k-th column or 0)."""
    return [((1, 1), 1.0, 0), ((7, 3), 1.0, 0), ((333, 777), 1.0, 0), ((4096, 1000), 1.0, 0),
            ((128, 8192), 1.0, 0), ((64, 2048), 1e4, 0), ((64, 2048), -1e4, 0), ((96, 1500), 30.0, 7),
            ((2048, 2048), 1.0, 0), ((5, 8191), 1.0, 0)]


def make_case(i, seed):
    shape, scale, inf_every = cases()[i]
    g = _Generator(device="cpu")
    g.manual_seed(seed)
    x = _randn(*shape, generator=g) * scale
    if scale < 0:
        x = x - 1e4
    if inf_every:
        x[:, ::inf_every] = float("-inf")
    return x.cuda().contiguous()


def make_inputs(seed, calls):
    g = _Generator(device="cuda")
    g.manual_seed(seed)
    return [_randn(4096, 4096, device="cuda", generator=g) for _ in range(calls)]


def load(path):
    spec = importlib.util.spec_from_file_location("module_under_test", path)
    mod = importlib.util.module_from_spec(spec)
    sys.path.insert(0, path.rsplit("/", 1)[0])
    spec.loader.exec_module(mod)
    return mod.softmax


def protocol():
    """The reply channel: a private copy of stdout. The module's own prints
    (and anything else written to fd 1) go to stderr instead, so they can
    never be read as a reply."""
    out = os.fdopen(os.dup(1), "w")
    os.dup2(2, 1)
    sys.stdout = sys.stderr
    return out


def main():
    out = protocol()
    install_guard()
    try:
        fn = load(sys.argv[1])
    except Exception as e:  # the module does not even import
        print(f"error import: {type(e).__name__}: {str(e)[:300]}", file=out, flush=True)
        return
    print("ready", file=out, flush=True)
    inputs, outputs = [], []
    for line in sys.stdin:
        cmd = line.split()
        if not cmd:
            continue
        try:
            if cmd[0] == "check":
                y = fn(make_case(int(cmd[1]), int(cmd[2])))
                _sync()
                _save(y.detach().cpu() if isinstance(y, torch.Tensor) else None, f"/tmp/check-{cmd[1]}.pt")
                reply = "ok"
            elif cmd[0] == "prep":
                inputs = outputs = None  # free the last round's buffers first
                inputs, outputs = make_inputs(int(cmd[1]), int(cmd[2])), []
                _sync()
                reply = "prepped"
            elif cmd[0] == "go":
                hooked = _fn_stack() + _dispatch_stack()
                outputs = [fn(x) for x in inputs]
                _sync()
                reply = f"done hooked={hooked}"
            elif cmd[0] == "dump":
                _save(outputs[int(cmd[1])].detach().cpu(), f"/tmp/dump-{cmd[1]}.pt")
                reply = "dumped"
            elif cmd[0] == "quit":
                return
            else:
                reply = "error unknown command"
        except Exception as e:
            reply = f"error {type(e).__name__}: {str(e)[:300]}"
        if FORBIDDEN:
            reply = "forbidden " + ",".join(sorted(set(FORBIDDEN)))
        print(reply, file=out, flush=True)


if __name__ == "__main__":
    main()
