import os
import subprocess
import sys

import pytest

import gatekeeper
from gatekeeper import ManualClock, SlidingWindowLimiter


def make(limit, window):
    clock = ManualClock()
    return SlidingWindowLimiter(limit=limit, window=window, clock=clock), clock


def test_request_exactly_window_old_no_longer_counts():
    limiter, clock = make(limit=2, window=10)
    assert limiter.allow("k") is True
    assert limiter.allow("k") is True
    clock.set(10)
    assert limiter.allow("k") is True
    assert limiter.allow("k") is True
    assert limiter.allow("k") is False


def test_request_just_inside_window_still_counts():
    limiter, clock = make(limit=2, window=10)
    limiter.allow("k")
    clock.set(0.5)
    limiter.allow("k")
    clock.set(9.75)
    assert limiter.allow("k") is False
    clock.set(10)
    assert limiter.allow("k") is True
    assert limiter.allow("k") is False
    clock.set(10.25)
    assert limiter.allow("k") is False
    clock.set(10.5)
    assert limiter.allow("k") is True


def test_remaining_uses_the_same_boundary():
    limiter, clock = make(limit=2, window=10)
    limiter.allow("k")
    clock.set(5)
    limiter.allow("k")
    assert limiter.remaining("k") == 0
    clock.set(10)
    assert limiter.remaining("k") == 1
    assert limiter.remaining("k") == 1
    clock.set(15)
    assert limiter.remaining("k") == 2
    assert limiter.remaining("other") == 2


def test_retry_after_is_exact_and_sufficient():
    limiter, clock = make(limit=2, window=10)
    limiter.allow("k")
    clock.set(4)
    limiter.allow("k")
    clock.set(6)
    wait = limiter.retry_after("k")
    assert wait == pytest.approx(4.0, abs=1e-9)
    clock.advance(wait)
    assert limiter.retry_after("k") == 0.0
    assert limiter.allow("k") is True
    assert limiter.retry_after("k") == pytest.approx(4.0, abs=1e-9)


def test_refused_requests_are_not_recorded():
    limiter, clock = make(limit=1, window=10)
    assert limiter.allow("k") is True
    clock.set(5)
    assert limiter.allow("k") is False
    clock.set(10)
    assert limiter.allow("k") is True
    assert limiter.remaining("k") == 0


def test_simulate_cli_follows_the_rule(tmp_path):
    root = os.path.dirname(os.path.dirname(os.path.abspath(gatekeeper.__file__)))
    env = {k: v for k, v in os.environ.items() if not k.startswith("GATEKEEPER_")}
    env["PYTHONPATH"] = root
    result = subprocess.run(
        [sys.executable, "-m", "gatekeeper", "simulate", "--limit", "1", "--window", "30",
         "a@0", "a@29.5", "a@30", "b@30", "a@59.75", "a@60"],
        cwd=tmp_path, env=env, capture_output=True, text=True, timeout=60,
    )
    assert result.returncode == 0, result.stderr
    assert result.stdout.splitlines() == [
        "a@0 allow remaining=0",
        "a@29.5 deny remaining=0",
        "a@30 allow remaining=0",
        "b@30 allow remaining=0",
        "a@59.75 deny remaining=0",
        "a@60 allow remaining=0",
    ]
