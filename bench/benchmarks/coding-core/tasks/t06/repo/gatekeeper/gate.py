"""Admission decisions driven by configuration.

A :class:`Gate` applies the ``limiter`` section of a configuration (see
:mod:`gatekeeper.config`): it can be switched off, some keys can be exempt,
and everything else goes through a :class:`SlidingWindowLimiter`.
"""

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

from .clock import Clock
from .config import default_config
from .limiter import SlidingWindowLimiter


@dataclass(frozen=True)
class Decision:
    """The outcome of :meth:`Gate.check` for one request."""

    allowed: bool
    remaining: int
    retry_after: float


class Gate:
    """Decide whether requests are admitted, according to a configuration.

    ``config`` is a mapping shaped like :func:`gatekeeper.load_config`'s
    result; only its ``limiter`` section is used. ``None`` means defaults.
    """

    def __init__(self, config: Mapping[str, Any] | None = None, clock: Clock | None = None) -> None:
        settings = (config if config is not None else default_config())["limiter"]
        self.enabled = bool(settings["enabled"])
        self.exempt_keys = frozenset(settings["exempt_keys"])
        self.limiter = SlidingWindowLimiter(settings["limit"], settings["window"], clock)

    def check(self, key: str) -> Decision:
        """Decide on one request for ``key`` and record it if admitted.

        Exempt keys, and every key while the gate is disabled, are always
        admitted and never recorded.
        """
        if not self.enabled or key in self.exempt_keys:
            return Decision(allowed=True, remaining=self.limiter.limit, retry_after=0.0)
        allowed = self.limiter.allow(key)
        return Decision(
            allowed=allowed,
            remaining=self.limiter.remaining(key),
            retry_after=self.limiter.retry_after(key),
        )
