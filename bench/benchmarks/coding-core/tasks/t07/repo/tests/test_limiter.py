import pytest

from gatekeeper import ManualClock, SlidingWindowLimiter


def make(limit=3, window=10.0):
    clock = ManualClock()
    return SlidingWindowLimiter(limit=limit, window=window, clock=clock), clock


def test_admits_up_to_limit_then_refuses():
    limiter, clock = make(limit=3)
    assert [limiter.allow("a") for _ in range(4)] == [True, True, True, False]
    clock.advance(1)
    assert limiter.allow("a") is False


def test_keys_are_independent():
    limiter, _ = make(limit=1)
    assert limiter.allow("a") is True
    assert limiter.allow("b") is True
    assert limiter.allow("a") is False


def test_requests_expire_long_after_window():
    limiter, clock = make(limit=2, window=10)
    limiter.allow("a")
    limiter.allow("a")
    clock.advance(25)
    assert limiter.allow("a") is True
    assert limiter.allow("a") is True
    assert limiter.allow("a") is False


def test_remaining_counts_down_and_never_records():
    limiter, clock = make(limit=3)
    assert limiter.remaining("a") == 3
    limiter.allow("a")
    clock.advance(2)
    limiter.allow("a")
    assert limiter.remaining("a") == 1
    assert limiter.remaining("a") == 1


def test_retry_after_zero_when_room_and_positive_when_full():
    limiter, clock = make(limit=1, window=10)
    assert limiter.retry_after("a") == 0.0
    limiter.allow("a")
    clock.advance(3)
    assert limiter.retry_after("a") > 0


def test_reset_one_key_or_all():
    limiter, _ = make(limit=1)
    limiter.allow("a")
    limiter.allow("b")
    limiter.reset("a")
    assert limiter.allow("a") is True
    assert limiter.allow("b") is False
    limiter.reset()
    assert limiter.allow("b") is True


@pytest.mark.parametrize("limit, window", [(0, 10), (-1, 10), (2, 0), (2, -5), (True, 10)])
def test_rejects_bad_arguments(limit, window):
    with pytest.raises(ValueError):
        SlidingWindowLimiter(limit=limit, window=window, clock=ManualClock())


def test_manual_clock_only_moves_forward():
    clock = ManualClock(5)
    assert clock.advance(2.5) == 7.5
    assert clock.set(9) == 9.0
    with pytest.raises(ValueError):
        clock.set(1)
    with pytest.raises(ValueError):
        clock.advance(-1)
