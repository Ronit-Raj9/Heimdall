import copy
import random
import signal
import time
from contextlib import contextmanager

import pytest

from gatekeeper import drop_duplicates, find_duplicates

TIME_LIMIT_S = 10.0
LARGE_SIZE = 200_000


@contextmanager
def deadline(seconds):
    """Fail the test if the block runs longer than ``seconds``."""

    def on_alarm(signum, frame):
        pytest.fail(f"call did not return within {seconds} s", pytrace=False)

    previous = signal.signal(signal.SIGALRM, on_alarm)
    signal.setitimer(signal.ITIMER_REAL, seconds)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous)


def test_normalisation_rules():
    records = [
        {"email": "  Ada@Example.COM "},   # 0
        {"email": "Strasse 5"},            # 1
        {"email": "a d a"},                # 2
        {"email": "ada@example.com"},      # 3
        {"email": "STRASSE\t5"},           # 4
        {"email": "A  D\nA"},              # 5
        {"email": 42},                     # 6
        {"email": "straße  5"},            # 7
        {"email": " 42 "},                 # 8
        {"email": "42.0"},                 # 9
    ]
    assert find_duplicates(records, ["email"]) == [[0, 3], [1, 4, 7], [2, 5], [6, 8]]


def test_empty_and_missing_values_never_match():
    records = [
        {"email": None, "phone": ""},
        {"email": "", "phone": "   "},
        {"phone": None},
        {},
        {"email": " \t ", "phone": "\n"},
        {"email": "x@y.z"},
        {"email": "X@Y.Z", "phone": ""},
    ]
    assert find_duplicates(records, ["email", "phone"]) == [[5, 6]]


def test_values_compared_only_within_the_same_field():
    records = [
        {"email": "shared", "phone": "1"},
        {"email": "2", "phone": "shared"},
        {"email": "3", "phone": "Shared "},
    ]
    assert find_duplicates(records, ["email", "phone"]) == [[1, 2]]


def test_matching_is_transitive_and_groups_are_ordered():
    records = [
        {"email": "e1", "phone": "p1"},   # 0
        {"email": "e2", "phone": "p2"},   # 1
        {"email": "e3", "phone": "p3"},   # 2
        {"email": "e4", "phone": "p2"},   # 3  joins 1 by phone
        {"email": "e1", "phone": "p4"},   # 4  joins 0 by email
        {"email": "e9", "phone": "p9"},   # 5  alone so far
        {"email": "e4", "phone": "p4"},   # 6  bridges {0, 4} and {1, 3}
        {"email": "e9", "phone": "p8"},   # 7  joins 5
        {"email": "e3", "phone": "p7"},   # 8  joins 2
        {"email": "e8", "phone": "p8"},   # 9  joins {5, 7}
    ]
    assert find_duplicates(records, ["email", "phone"]) == [
        [0, 1, 3, 4, 6],
        [2, 8],
        [5, 7, 9],
    ]
    # Only the email field: the phone links no longer count.
    assert find_duplicates(records, ["email"]) == [[0, 4], [2, 8], [3, 6], [5, 7]]


def test_drop_duplicates_keeps_first_of_each_group_in_order():
    records = [
        {"id": 0, "email": "a", "phone": "1"},
        {"id": 1, "email": "b", "phone": "2"},
        {"id": 2, "email": "c", "phone": "1"},
        {"id": 3, "email": "", "phone": ""},
        {"id": 4, "email": "C", "phone": "9"},
        {"id": 5, "email": "", "phone": None},
        {"id": 6, "email": "B", "phone": "8"},
    ]
    before = copy.deepcopy(records)
    kept = drop_duplicates(records, ["email", "phone"])
    assert [r["id"] for r in kept] == [0, 1, 3, 5]
    assert records == before


def build_large(seed=20261114):
    """Return (records, expected groups) with exactly known duplicate groups."""
    rng = random.Random(seed)
    items = []  # (record, label); records with the same label form one group
    for i in range(130_000):
        items.append(({"email": f"user{i}@example.com", "phone": f"+1-555-{i:07d}"}, ("solo", i)))
    for g in range(10_000):
        base = f"member{g}@example.org"
        variants = [base, base.upper(), f"\t{base}  "]
        for k, email in enumerate(variants):
            items.append(({"email": email, "phone": f"+44-{g:05d}-{k}"}, ("triple", g)))
    for k in range(20_000):
        # Record k shares its phone with k - 1 (odd k) or its email (even k):
        # one long chain that only transitive matching joins together.
        items.append(({"email": f"c{(k + 1) // 2}@chain.test", "phone": f"chain-{k // 2}"}, ("chain", 0)))
    blanks = [None, "", "   ", "\t"]
    for b in range(LARGE_SIZE - len(items)):
        record = {"email": blanks[b % 4]}
        if b % 3:
            record["phone"] = blanks[(b + 1) % 4]
        items.append((record, ("blank", b)))
    rng.shuffle(items)
    records = [record for record, _ in items]
    by_label = {}
    for index, (_, label) in enumerate(items):
        by_label.setdefault(label, []).append(index)
    expected = sorted((members for members in by_label.values() if len(members) > 1), key=lambda m: m[0])
    return records, expected


@pytest.fixture(scope="module")
def large():
    records, expected = build_large()
    assert len(records) == LARGE_SIZE
    return records, expected


def test_find_duplicates_large_input_is_fast_and_exact(large):
    records, expected = large
    with deadline(TIME_LIMIT_S):
        start = time.perf_counter()
        result = find_duplicates(records, ["email", "phone"])
        elapsed = time.perf_counter() - start
    assert elapsed < TIME_LIMIT_S
    assert len(result) == len(expected) == 10_001
    assert result == expected


def test_drop_duplicates_large_input_is_fast_and_exact(large):
    records, expected = large
    dropped = {index for group in expected for index in group[1:]}
    with deadline(TIME_LIMIT_S):
        start = time.perf_counter()
        kept = drop_duplicates(records, ["email", "phone"])
        elapsed = time.perf_counter() - start
    assert elapsed < TIME_LIMIT_S
    assert len(kept) == LARGE_SIZE - len(dropped)
    assert kept == [record for index, record in enumerate(records) if index not in dropped]
