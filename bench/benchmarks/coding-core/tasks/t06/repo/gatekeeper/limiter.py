"""Sliding-window rate limiting.

A :class:`SlidingWindowLimiter` admits at most ``limit`` requests per key in
any window of ``window`` seconds. Time comes from an injectable clock (see
:mod:`gatekeeper.clock`), so tests can control it exactly.

Window rule: at time ``now``, a request recorded at time ``t`` is inside the
window when ``now - window < t <= now``. A request made exactly ``window``
seconds ago has left the window. Refused requests are never recorded.
"""

from bisect import bisect_right

from .clock import Clock, SystemClock


class SlidingWindowLimiter:
    """Admit at most ``limit`` requests per key in any ``window`` seconds.

    >>> from gatekeeper.clock import ManualClock
    >>> clock = ManualClock()
    >>> limiter = SlidingWindowLimiter(limit=2, window=10, clock=clock)
    >>> limiter.allow("alice"), limiter.allow("alice"), limiter.allow("alice")
    (True, True, False)
    """

    def __init__(self, limit: int, window: float, clock: Clock | None = None) -> None:
        if isinstance(limit, bool) or not isinstance(limit, int) or limit < 1:
            raise ValueError(f"limit must be a positive integer, got {limit!r}")
        if isinstance(window, bool) or not isinstance(window, (int, float)) or window <= 0:
            raise ValueError(f"window must be a positive number of seconds, got {window!r}")
        self.limit = limit
        self.window = float(window)
        self.clock = clock if clock is not None else SystemClock()
        # Per key, the times of admitted requests in ascending order. The
        # clock never goes backwards, so appending keeps each list sorted.
        self._events: dict[str, list[float]] = {}

    def allow(self, key: str) -> bool:
        """Admit and record a request for ``key`` if there is room.

        Returns ``True`` when the request is admitted. A refused request is
        not recorded, so it does not count against later requests.
        """
        now = self.clock.now()
        events = self._live_events(key, now)
        if len(events) >= self.limit:
            return False
        events.append(now)
        return True

    def remaining(self, key: str) -> int:
        """Return how many more requests ``key`` may make right now.

        This only reads the state; it never records anything.
        """
        now = self.clock.now()
        return self.limit - self._count(key, now)

    def retry_after(self, key: str) -> float:
        """Return the seconds until a request for ``key`` would be admitted.

        Returns ``0.0`` when a request would be admitted now.
        """
        now = self.clock.now()
        events = self._live_events(key, now)
        if len(events) < self.limit:
            return 0.0
        # The request that has to leave the window before another one fits.
        blocking = events[len(events) - self.limit]
        return max(0.0, blocking + self.window - now)

    def reset(self, key: str | None = None) -> None:
        """Forget recorded requests for ``key``, or for every key if ``None``."""
        if key is None:
            self._events.clear()
        else:
            self._events.pop(key, None)

    def __repr__(self) -> str:
        return f"SlidingWindowLimiter(limit={self.limit!r}, window={self.window!r})"

    def _live_events(self, key: str, now: float) -> list[float]:
        """Drop requests for ``key`` that left the window; return the rest."""
        events = self._events.setdefault(key, [])
        expired = bisect_right(events, now - self.window)
        if expired:
            del events[:expired]
        return events

    def _count(self, key: str, now: float) -> int:
        """Count requests for ``key`` inside the window, without pruning."""
        events = self._events.get(key, [])
        return len(events) - bisect_right(events, now - self.window)
