"""Child process of verify.py for an imported KernelBench problem.

Usage: python -I -B harness.py ref|new

ref builds the problem's Model from the verifier's own copy of problem.py;
new builds ModelNew from the workspace's solution.py (the agent's code).
Both are built with torch.manual_seed(INIT_SEED) first, as KernelBench
does, so their parameters start equal. It answers verify.py over stdin and
stdout, one line per command:

  check <seed>          inputs = get_inputs() under <seed>; save the output to /tmp
  prep <seed> <calls>   make <calls> input sets under <seed>, on the GPU
  go                    run the model once on each prepared set, then sync
  dump <k>              save the output of call k to /tmp
  quit

verify.py holds the clock and the reference outputs, so this process never
reports a time or a verdict that is trusted.
"""
import importlib.util
import os
import sys

TESTS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, TESTS)

import torch  # noqa: E402

import problem  # noqa: E402  (the verifier's copy)

INIT_SEED = 42
_sync = torch.cuda.synchronize
_save = torch.save
_seed = torch.manual_seed
_fn_stack = torch._C._len_torch_function_stack
_dispatch_stack = torch._C._len_torch_dispatch_stack


def protocol():
    out = os.fdopen(os.dup(1), "w")
    os.dup2(2, 1)
    sys.stdout = sys.stderr
    return out


def to_cuda(xs):
    return [x.cuda() if isinstance(x, torch.Tensor) else x for x in xs]


def inputs(seed):
    _seed(seed)
    return to_cuda(problem.get_inputs())


def detach(y):
    if isinstance(y, torch.Tensor):
        return y.detach().cpu()
    if isinstance(y, (list, tuple)):
        return [detach(v) for v in y]
    return y


def build(which):
    init = problem.get_init_inputs()
    if which == "ref":
        cls = problem.Model
    else:
        sys.path.append("/workspace")
        spec = importlib.util.spec_from_file_location("solution", "/workspace/solution.py")
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        cls = mod.ModelNew
    _seed(INIT_SEED)
    return cls(*init).cuda().eval()


def main():
    out = protocol()
    try:
        model = build(sys.argv[1])
    except Exception as e:
        print(f"error build: {type(e).__name__}: {str(e)[:300]}", file=out, flush=True)
        return
    print("ready", file=out, flush=True)
    prepared, outputs = [], []
    for line in sys.stdin:
        cmd = line.split()
        if not cmd:
            continue
        try:
            with torch.no_grad():
                if cmd[0] == "check":
                    y = model(*inputs(int(cmd[1])))
                    _sync()
                    _save(detach(y), "/tmp/kb-check.pt")
                    reply = "ok"
                elif cmd[0] == "prep":
                    prepared = outputs = None  # free the last round's buffers first
                    _seed(int(cmd[1]))
                    prepared = [to_cuda(problem.get_inputs()) for _ in range(int(cmd[2]))]
                    outputs = []
                    _sync()
                    reply = "prepped"
                elif cmd[0] == "go":
                    hooked = _fn_stack() + _dispatch_stack()
                    outputs = [model(*xs) for xs in prepared]
                    _sync()
                    reply = f"done hooked={hooked}"
                elif cmd[0] == "dump":
                    _save(detach(outputs[int(cmd[1])]), f"/tmp/kb-dump-{cmd[1]}.pt")
                    reply = "dumped"
                elif cmd[0] == "quit":
                    return
                else:
                    reply = "error unknown command"
        except Exception as e:
            reply = f"error {type(e).__name__}: {str(e)[:300]}"
        print(reply, file=out, flush=True)


if __name__ == "__main__":
    main()
