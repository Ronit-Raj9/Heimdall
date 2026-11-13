"""gatekeeper: small building blocks for request admission.

The public API is everything listed in ``__all__`` below:

* rate limiting: :class:`SlidingWindowLimiter`, :class:`Gate`, :class:`Decision`
* clocks: :class:`SystemClock`, :class:`ManualClock`
* configuration: :func:`load_config`, :func:`default_config`, :data:`DEFAULTS`
* duplicate detection: :func:`normalise_key`, :func:`find_duplicates`,
  :func:`drop_duplicates`
* errors: :class:`GatekeeperError`, :class:`ConfigError`

Submodules are implementation details unless a name is re-exported here.
"""

from .clock import ManualClock, SystemClock
from .config import DEFAULTS, default_config, load_config
from .dedupe import drop_duplicates, find_duplicates, normalise_key
from .errors import ConfigError, GatekeeperError
from .gate import Decision, Gate
from .limiter import SlidingWindowLimiter

__version__ = "0.4.0"

__all__ = [
    "ConfigError",
    "DEFAULTS",
    "Decision",
    "Gate",
    "GatekeeperError",
    "ManualClock",
    "SlidingWindowLimiter",
    "SystemClock",
    "default_config",
    "drop_duplicates",
    "find_duplicates",
    "load_config",
    "normalise_key",
]
