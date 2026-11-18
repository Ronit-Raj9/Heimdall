"""Synthetic training data for the churn classifier, generated deterministically.

make(data_seed) builds a random "teacher" function from data_seed and labels
20,000 Gaussian points with it; the same seed always gives the same data.
Nothing is downloaded.
"""
import numpy as np

FEATURES, CLASSES, HIDDEN, SCALE, MARGIN = 16, 10, 32, 1.5, 0.5


def teacher(data_seed):
    r = np.random.default_rng(data_seed)
    w1 = r.normal(size=(FEATURES, HIDDEN)) * (SCALE / np.sqrt(FEATURES))
    b1 = r.uniform(-np.pi, np.pi, size=HIDDEN)
    w2 = r.normal(size=(HIDDEN, CLASSES))
    return w1, b1, w2


def sample(t, sample_seed, n):
    w1, b1, w2 = t
    r = np.random.default_rng(sample_seed)
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


def make(data_seed, n=20000):
    """The training set: n points labelled by the teacher of data_seed."""
    return sample(teacher(data_seed), data_seed + 1, n)
