"""Hidden verifier of an imported KernelBench problem, on the run's GPU.

KernelBench's own rule (src/kernelbench/eval.py at 423217d9): ModelNew is
correct when its output matches Model's within atol = rtol = 1e-2 (float32)
on every one of several random input draws, both models built after the
same seed; it is then timed against Model. Here the agent's code runs only
in a child process (harness.py new); the reference model runs in another
(harness.py ref) and in this process, which holds the clock and computes
the reference outputs itself.

1. Correctness: CORRECT_TRIALS input draws, seeded from os.urandom here.
2. Speed: ROUNDS timed rounds per model after a warm-up round, alternating
   Model and ModelNew; each round makes CALLS fresh input sets in the child,
   this process times "go", and checks one output it picks at random against
   its own reference output. The speedup is the median per-round ratio; the
   interval is the 2nd smallest and 2nd largest of 9 ratios (about 96% for
   the median, distribution-free).

Reward 1 when correct and, if kb.json sets a speedup_target above 0, when
the interval's lower end reaches it (KernelBench's fast_p with p the
target; 0 is fast_0, correctness only). Dimensions: correct, speedup,
speedup_lo, speedup_hi, ref_ms, agent_ms.
"""
import json
import os
import statistics
import subprocess
import sys
import time

TESTS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, TESTS)

import torch  # noqa: E402

import harness  # noqa: E402  (its input maker and model builder; no agent code)

OUT = "/grader/out/reward.json"
with open(os.path.join(TESTS, "kb.json")) as f:
    CFG = json.load(f)
TOL = CFG.get("tolerance", 1e-2)
CORRECT_TRIALS = CFG.get("correct_trials", 5)
ROUNDS = 9
CALLS = max(1, int(CFG.get("calls", 4)))
TARGET = float(CFG.get("speedup_target", 0))


def write(reward, dims, note):
    with open(OUT, "w") as f:
        json.dump({"reward": reward, "dimensions": {k: float(v) for k, v in dims.items()}}, f)
    print(note, json.dumps(dims), flush=True)


class Child:
    def __init__(self, which):
        self.p = subprocess.Popen([sys.executable, "-I", "-B", os.path.join(TESTS, "harness.py"), which],
                                  stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True, cwd="/tmp",
                                  start_new_session=True)
        self.first = self.read()

    def read(self):
        line = self.p.stdout.readline()
        return line.strip() if line else "error the child exited"

    def ask(self, cmd):
        try:
            self.p.stdin.write(cmd + "\n")
            self.p.stdin.flush()
        except OSError as e:
            return f"error {e}"
        return self.read()

    def close(self):
        try:
            os.killpg(self.p.pid, 9)
        except OSError:
            pass
        self.p.wait()


def load_out(path):
    """Loads a saved output and removes the file: /tmp is a small tmpfs, and
    a large problem's output fills it after one or two saves."""
    try:
        return torch.load(path, weights_only=True)
    finally:
        try:
            os.remove(path)
        except OSError:
            pass


def seed():
    return int.from_bytes(os.urandom(4), "little") % (2**31)


def same(a, b):
    if isinstance(a, (list, tuple)):
        return isinstance(b, (list, tuple)) and len(a) == len(b) and all(same(x, y) for x, y in zip(a, b))
    if isinstance(a, torch.Tensor):
        return (isinstance(b, torch.Tensor) and a.shape == b.shape and a.dtype == b.dtype
                and bool(torch.allclose(a.float(), b.float(), atol=TOL, rtol=TOL)))
    return a == b


def main():
    dims = {"correct": 0}
    if not os.path.isfile("/workspace/solution.py"):
        return write(0, dims, "solution.py is missing")
    ref_model = harness.build("ref")
    new = Child("new")
    if new.first != "ready":
        new.close()
        return write(0, dims, "ModelNew does not build: " + new.first)

    def reference(s, k=0):
        torch.manual_seed(s)
        xs = None
        for _ in range(k + 1):
            xs = harness.to_cuda(harness.problem.get_inputs())
        with torch.no_grad():
            y = harness.detach(ref_model(*xs))
        torch.cuda.synchronize()
        return y

    for i in range(CORRECT_TRIALS):
        s = seed()
        reply = new.ask(f"check {s}")
        if reply != "ok":
            new.close()
            if "OutOfMemoryError" in reply:
                dims["out_of_memory"] = 1  # the problem does not fit this GPU as written
            return write(0, dims, f"trial {i}: {reply}")
        if not same(load_out("/tmp/kb-check.pt"), reference(s)):
            new.close()
            return write(0, dims, f"trial {i}: output differs from Model beyond atol=rtol={TOL}")
    dims["correct"] = 1
    base = Child("ref")
    if base.first != "ready":
        new.close()
        base.close()
        raise SystemExit("the reference child did not start: " + base.first)
    times = {"ref": [], "new": []}
    for r in range(ROUNDS + 1):
        for name, child in (("ref", base), ("new", new)):
            s = seed()
            if child.ask(f"prep {s} {CALLS}") != "prepped":
                new.close()
                base.close()
                return write(0, dims, f"{name} round {r}: inputs not prepared")
            t0 = time.perf_counter()
            reply = child.ask("go")
            dt = time.perf_counter() - t0
            if reply != "done hooked=0":
                new.close()
                base.close()
                return write(0, dims, f"{name} round {r}: {reply}")
            k = int.from_bytes(os.urandom(1), "little") % CALLS
            if child.ask(f"dump {k}") != "dumped":
                new.close()
                base.close()
                return write(0, dims, f"{name} round {r}: output not saved")
            if not same(load_out(f"/tmp/kb-dump-{k}.pt"), reference(s, k)):
                dims["correct"] = 0
                new.close()
                base.close()
                return write(0, dims, f"{name} round {r}: a timed call returned a wrong result")
            if r > 0:
                times[name].append(dt / CALLS * 1000)
    new.close()
    base.close()
    ratios = sorted(a / b for a, b in zip(times["ref"], times["new"]))
    dims.update(speedup=statistics.median(ratios), speedup_lo=ratios[1], speedup_hi=ratios[-2],
                ref_ms=statistics.median(times["ref"]), agent_ms=statistics.median(times["new"]))
    reward = 1 if TARGET <= 0 or dims["speedup_lo"] >= TARGET else 0
    write(reward, dims, "passed" if reward else f"speedup interval lower end {dims['speedup_lo']:.2f} is below {TARGET}")


if __name__ == "__main__":
    main()
