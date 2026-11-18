"""Hidden verifier of fix-slow-loop. Runs in the clean room on the run's GPU.

The agent's train.py never runs in this process: train_harness.py imports it
in a child process, with the verifier's own data.py and model.py. This
process holds the clock and scores the weights after every child is gone.

One child process runs the original loop (reference_train.py) and one the
agent's, each warmed up on the GPU before its first round. For each of
ROUNDS rounds, a fresh data seed from os.urandom builds the training data
and both loops train once on it, in alternating order. This process times
each call from "go" to "done": the call itself, with the GPU synchronized,
never a child's start-up or imports. After the last
round every other process in the container is killed; only then are the
held-out points drawn, with a second fresh seed, from the same data, and
both models of each round are scored on them. Held-out points therefore
never exist while any of the agent's code runs.

Reward 1 when, in every round, the agent's model is at most TOLERANCE below
the original's held-out accuracy and the median per-round speedup is at
least TARGET. On a shared, loaded machine one round can be much slower than
the others, so the rule reads the median and reports the range. Dimensions: accuracy_kept, heldout_score (median agent
accuracy), heldout_ref, speedup (median ratio), speedup_lo and speedup_hi
(the smallest and largest of the rounds), time_to_target_s (median agent
seconds, when accuracy was kept), ref_s.
"""
import io
import json
import os
import statistics
import subprocess
import sys
import time

TESTS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, TESTS)

import data  # noqa: E402  (numpy only; torch is imported once every child is gone)

WORKSPACE = "/workspace"
OUT = "/grader/out/reward.json"
ROUNDS = 3
TARGET = 2.5
TOLERANCE = 0.015
HELDOUT = 10000


def write(reward, dims, note):
    with open(OUT, "w") as f:
        json.dump({"reward": reward, "dimensions": {k: float(v) for k, v in dims.items()}}, f)
    print(note, json.dumps(dims), flush=True)


def seed():
    return int.from_bytes(os.urandom(4), "little") % (2**31)


class Child:
    """One train_harness.py process for one loop, used for every round.

    The weights are read the moment "done" arrives, so a reply sent before
    training finished (the round's weights file would not exist yet) fails,
    and nothing the child does later changes what is scored. The child's
    whole process group is killed at the end.
    """

    def __init__(self, train_py):
        self.p = subprocess.Popen([sys.executable, "-I", "-B", os.path.join(TESTS, "train_harness.py"), train_py],
                                  stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True,
                                  cwd="/tmp", start_new_session=True)
        self.first = self.p.stdout.readline().strip() or "the child exited before it was ready"

    def train(self, data_seed, out):
        """Returns (seconds, reply, weights bytes or None)."""
        try:
            t0 = time.perf_counter()
            self.p.stdin.write(f"go {data_seed} {out}\n")
            self.p.stdin.flush()
            reply = self.p.stdout.readline().strip()
            dt = time.perf_counter() - t0
        except OSError as e:
            return None, f"error {e}", None
        try:
            with open(out, "rb") as f:
                weights = f.read()
        except OSError:
            weights = None
        return dt, reply or "the child exited", weights

    def close(self):
        try:
            os.killpg(self.p.pid, 9)
        except OSError:
            pass
        self.p.wait()


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


def accuracy(weights, data_seed, sample_seed):
    import torch

    from model import MLP

    m = MLP()
    m.load_state_dict(torch.load(io.BytesIO(weights), weights_only=True), strict=True)
    m = m.cuda().eval()
    x, y = data.sample(data.teacher(data_seed), sample_seed, HELDOUT)
    with torch.no_grad():
        pred = m(torch.from_numpy(x).cuda()).argmax(1).cpu()
    return float((pred == torch.from_numpy(y)).float().mean())


def main():
    dims = {"accuracy_kept": 0}
    agent_py = os.path.join(WORKSPACE, "train.py")
    if not os.path.isfile(agent_py):
        return write(0, dims, "train.py is missing")
    children = {"ref": Child(os.path.join(TESTS, "reference_train.py")), "agent": Child(agent_py)}
    for name, c in children.items():
        if c.first != "ready":
            for c2 in children.values():
                c2.close()
            kill_strays()
            if name == "ref":
                raise SystemExit("the original loop did not start: " + c.first)
            return write(0, dims, "the agent's train.py does not import: " + c.first)
    rounds = []
    for r in range(ROUNDS):
        ds = seed()
        order = ["ref", "agent"] if r % 2 == 0 else ["agent", "ref"]
        got = {}
        for name in order:
            dt, reply, weights = children[name].train(ds, f"/tmp/{name}-{r}.pt")
            if dt is None or reply != "done hooked=0" or weights is None:
                for c in children.values():
                    c.close()
                kill_strays()
                if name == "ref":
                    raise SystemExit(f"the original loop failed in round {r}: {reply}")
                return write(0, dims, f"the agent's train() failed in round {r}: {reply if weights is not None else 'no weights when it said done'}")
            got[name] = (dt, weights)
        rounds.append((ds, got))
    for c in children.values():
        c.close()
    kill_strays()
    acc_ref, acc_agent, ratios, agent_s = [], [], [], []
    for ds, got in rounds:
        ss = seed()
        try:
            a = accuracy(got["agent"][1], ds, ss)
        except Exception as e:  # not an MLP state dict
            return write(0, dims, f"the agent's model does not load into model.MLP(): {type(e).__name__}: {str(e)[:200]}")
        acc_agent.append(a)
        acc_ref.append(accuracy(got["ref"][1], ds, ss))
        ratios.append(got["ref"][0] / got["agent"][0])
        agent_s.append(got["agent"][0])
    kept = all(a >= b - TOLERANCE for a, b in zip(acc_agent, acc_ref))
    dims.update(accuracy_kept=int(kept), heldout_score=statistics.median(acc_agent), heldout_ref=statistics.median(acc_ref),
                speedup=statistics.median(ratios), speedup_lo=min(ratios), speedup_hi=max(ratios),
                ref_s=statistics.median(g["ref"][0] for _, g in rounds))
    if kept:
        dims["time_to_target_s"] = statistics.median(agent_s)
    reward = 1 if kept and dims["speedup"] >= TARGET else 0
    note = "passed" if reward else ("accuracy dropped" if not kept else f"median speedup {dims['speedup']:.2f} is below {TARGET}")
    write(reward, dims, note)


if __name__ == "__main__":
    main()
