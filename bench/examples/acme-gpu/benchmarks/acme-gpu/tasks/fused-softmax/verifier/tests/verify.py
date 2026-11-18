"""Hidden verifier of fused-softmax. Runs in the clean room on the run's GPU.

The agent's softmax.py (workspace with the agent's diff applied, read-only)
never runs in this process: harness.py imports it in a child process. This
process holds the clock and the float64 reference, so the child can only
answer, never grade.

1. Correctness: every case of harness.cases(), seeded from os.urandom here,
   must be within atol 1e-5 + rtol 1e-4 of the float64 softmax; PyTorch's own
   softmax is refused in the child.
2. Speed: a child with the original softmax (reference_softmax.py) and one
   with the agent's alternate ROUNDS timed rounds after a warm-up round.
   Each round, this process seeds CALLS fresh 4096 x 4096 inputs in the
   child, starts its clock, sends "go" and stops it when the child has run
   and synchronized every call; then it checks one output it picks at
   random against its own reference, so a fast but wrong round fails. The
   speedup is the median of the per-round ratios; the interval is the 2nd
   smallest and 2nd largest of the 9 ratios, a distribution-free interval
   of about 96% for the median.

Reward 1 when every case is correct and the interval's lower end is at
least TARGET. Dimensions: correct, allowed, speedup, speedup_lo, speedup_hi,
ref_ms, agent_ms.
"""
import json
import os
import statistics
import subprocess
import sys
import time

import torch

TESTS = os.path.dirname(os.path.abspath(__file__))
WORKSPACE = "/workspace"
OUT = "/grader/out/reward.json"
TARGET = 2.0
ROUNDS = 9
CALLS = 6
ATOL, RTOL = 1e-5, 1e-4
sys.path.insert(0, TESTS)
import harness  # noqa: E402  (only its case list and input maker; no agent code)


def write(reward, dims, note):
    dims = {k: float(v) for k, v in dims.items()}
    with open(OUT, "w") as f:
        json.dump({"reward": reward, "dimensions": dims}, f)
    print(note, json.dumps(dims), flush=True)


class Child:
    def __init__(self, module):
        self.p = subprocess.Popen([sys.executable, "-I", "-B", os.path.join(TESTS, "harness.py"), module],
                                  stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True, cwd="/tmp",
                                  start_new_session=True)
        self.first = self.read()

    def read(self):
        line = self.p.stdout.readline()
        return line.strip() if line else "error the child exited"

    def ask(self, cmd):
        self.p.stdin.write(cmd + "\n")
        self.p.stdin.flush()
        return self.read()

    def close(self):
        try:
            self.p.stdin.write("quit\n")
            self.p.stdin.flush()
        except OSError:
            pass
        try:
            self.p.wait(timeout=20)
        except subprocess.TimeoutExpired:
            self.p.kill()


def kill_strays():
    """Kill every process of this container but this one, so nothing the
    agent's code started outlives its turn."""
    me = os.getpid()
    for pid in os.listdir("/proc"):
        if pid.isdigit() and int(pid) != me:
            try:
                os.kill(int(pid), 9)
            except OSError:
                pass


def reference(x):
    return torch.softmax(x.double(), dim=1)


def close_enough(y, x):
    if not isinstance(y, torch.Tensor) or y.shape != x.shape or y.dtype != torch.float32:
        return False
    return bool(torch.allclose(y.double(), reference(x).cpu(), atol=ATOL, rtol=RTOL, equal_nan=False))


def seed():
    return int.from_bytes(os.urandom(4), "little")


def main():
    dims = {"correct": 0, "allowed": 1}
    module = os.path.join(WORKSPACE, "softmax.py")
    if not os.path.isfile(module):
        return write(0, dims, "softmax.py is missing")
    agent = Child(module)
    if agent.first != "ready":
        agent.close()
        return write(0, dims, "the agent's softmax.py does not import: " + agent.first)
    for i in range(len(harness.cases())):
        s = seed()
        reply = agent.ask(f"check {i} {s}")
        if reply.startswith("forbidden"):
            dims["allowed"] = 0
            agent.close()
            return write(0, dims, "uses PyTorch's own softmax: " + reply)
        if reply != "ok":
            agent.close()
            return write(0, dims, f"case {i} failed to run: {reply}")
        y = torch.load(f"/tmp/check-{i}.pt", weights_only=True)
        if not close_enough(y, harness.make_case(i, s).cpu()):
            agent.close()
            return write(0, dims, f"case {i} {harness.cases()[i]} is wrong")
    dims["correct"] = 1
    base = Child(os.path.join(TESTS, "reference_softmax.py"))
    if base.first != "ready":
        agent.close()
        base.close()
        raise SystemExit("reference child did not start: " + base.first)
    times = {"ref": [], "agent": []}
    for r in range(ROUNDS + 1):
        for name, child in (("ref", base), ("agent", agent)):
            s = seed()
            if child.ask(f"prep {s} {CALLS}") != "prepped":
                agent.close()
                base.close()
                return write(0, dims, f"{name} child failed to prepare inputs")
            t0 = time.perf_counter()
            reply = child.ask("go")
            dt = time.perf_counter() - t0
            if reply.startswith("forbidden"):
                dims["allowed"] = 0
                agent.close()
                base.close()
                return write(0, dims, "uses PyTorch's own softmax: " + reply)
            if not reply.startswith("done") or reply != "done hooked=0":
                agent.close()
                base.close()
                return write(0, dims, f"{name} round {r}: {reply}")
            k = int.from_bytes(os.urandom(1), "little") % CALLS
            if child.ask(f"dump {k}") != "dumped":
                agent.close()
                base.close()
                return write(0, dims, f"{name} round {r}: output not saved")
            g = torch.Generator(device="cuda")
            g.manual_seed(s)
            for _ in range(k + 1):  # the k-th draw of the same generator, one tensor at a time
                x = torch.randn(4096, 4096, device="cuda", generator=g)
            ok = close_enough(torch.load(f"/tmp/dump-{k}.pt", weights_only=True), x.cpu())
            del x
            if not ok:
                dims["correct"] = 0
                agent.close()
                base.close()
                return write(0, dims, f"{name} round {r}: call {k} returned a wrong result")
            if r > 0:
                times[name].append(dt / CALLS * 1000)
    agent.close()
    base.close()
    kill_strays()
    ratios = sorted(a / b for a, b in zip(times["ref"], times["agent"]))
    dims.update(speedup=statistics.median(ratios), speedup_lo=ratios[1], speedup_hi=ratios[-2],
                ref_ms=statistics.median(times["ref"]), agent_ms=statistics.median(times["agent"]))
    reward = 1 if dims["correct"] == 1 and dims["speedup_lo"] >= TARGET else 0
    write(reward, dims, "passed" if reward else f"speedup interval lower end {dims['speedup_lo']:.2f} is below {TARGET}")


if __name__ == "__main__":
    main()
