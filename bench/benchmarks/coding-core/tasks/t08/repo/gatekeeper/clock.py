"""Clocks used by the rate limiter.

Library code never reads the system time directly. It asks a clock for
``now()``, which returns a time in seconds as a float. Only differences
between two readings matter, so the origin is arbitrary.

:class:`SystemClock` is the production clock. :class:`ManualClock` only moves
when told to, which keeps tests and ``python -m gatekeeper simulate``
deterministic.
"""

import time
from typing import Protocol


class Clock(Protocol):
    """Anything with a ``now()`` method returning seconds as a float."""

    def now(self) -> float:
        """Return the current time in seconds."""
        ...


class SystemClock:
    """Monotonic time in seconds, from :func:`time.monotonic`."""

    def now(self) -> float:
        """Return the current monotonic time in seconds."""
        return time.monotonic()


class ManualClock:
    """A clock that only moves forward, and only when told to.

    >>> clock = ManualClock()
    >>> clock.advance(1.5)
    1.5
    >>> clock.now()
    1.5
    """

    def __init__(self, start: float = 0.0) -> None:
        self._now = float(start)

    def now(self) -> float:
        """Return the current time in seconds."""
        return self._now

    def advance(self, seconds: float) -> float:
        """Move the clock forward by ``seconds`` and return the new time."""
        if seconds < 0:
            raise ValueError(f"cannot move a clock backwards (advance by {seconds})")
        self._now += float(seconds)
        return self._now

    def set(self, when: float) -> float:
        """Move the clock forward to ``when`` and return it.

        Raises :class:`ValueError` if ``when`` is earlier than the current time.
        """
        when = float(when)
        if when < self._now:
            raise ValueError(f"cannot move a clock backwards (from {self._now} to {when})")
        self._now = when
        return self._now

    def __repr__(self) -> str:
        return f"ManualClock({self._now!r})"
