"""The data source of train-classifier. Hidden: only the verifier has it.

A random two-layer "teacher" (tanh features, then a linear map) labels
Gaussian points with one of 10 classes; points too close to a class
boundary are dropped, so a perfect classifier exists. The workspace's
train.npz holds TRAIN points from this source, written once with
`python gen.py train.npz` in the task's own image; the verifier draws the
held-out points from the same teacher with HELDOUT_SEED. Neither seed, nor
the teacher, is ever in the agent's container.
"""
import sys

import numpy as np

FEATURES, CLASSES, HIDDEN, SCALE, MARGIN = 16, 10, 32, 2.5, 0.5
TEACHER_SEED = 424242
TRAIN_SEED, TRAIN = 1001, 20000
HELDOUT_SEED, HELDOUT = 909090, 10000


def teacher():
    r = np.random.default_rng(TEACHER_SEED)
    w1 = r.normal(size=(FEATURES, HIDDEN)) * (SCALE / np.sqrt(FEATURES))
    b1 = r.uniform(-np.pi, np.pi, size=HIDDEN)
    w2 = r.normal(size=(HIDDEN, CLASSES))
    return w1, b1, w2


def sample(seed, n):
    w1, b1, w2 = teacher()
    r = np.random.default_rng(seed)
    xs, ys, got = [], [], 0
    while got < n:
        x = r.normal(size=(2 * n, FEATURES))
        f = np.tanh(x @ w1 + b1) @ w2
        s = np.sort(f, axis=1)
        keep = (s[:, -1] - s[:, -2]) > MARGIN
        xs.append(x[keep])
        ys.append(f[keep].argmax(axis=1))
        got += int(keep.sum())
    return np.concatenate(xs)[:n].astype(np.float32), np.concatenate(ys)[:n].astype(np.int64)


if __name__ == "__main__":
    x, y = sample(TRAIN_SEED, TRAIN)
    np.savez_compressed(sys.argv[1], x=x.astype(np.float16), y=y.astype(np.uint8))
