"""Exceptions raised by gatekeeper."""


class GatekeeperError(Exception):
    """Base class for every error gatekeeper raises on purpose.

    The CLI catches this class, prints ``gatekeeper: error: <message>`` to
    stderr and exits with status 1.
    """


class ConfigError(GatekeeperError, ValueError):
    """A configuration file, value or environment variable is invalid."""
