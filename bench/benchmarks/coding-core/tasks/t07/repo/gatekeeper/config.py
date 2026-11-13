"""Configuration loading.

Settings come from two layers. The file wins, setting by setting:

1. built-in defaults (:data:`DEFAULTS`),
2. an optional TOML file.

Every setting has the type of its default. File values must already have that
type (an integer is accepted where a float is expected).
"""

import copy
import os
import tomllib
from collections.abc import Mapping
from typing import Any

from .errors import ConfigError

DEFAULTS: dict[str, Any] = {
    "name": "gatekeeper",
    "limiter": {
        "enabled": True,
        "limit": 60,
        "window": 60.0,
        "exempt_keys": [],
    },
    "dedupe": {
        "keys": ["email"],
    },
    "log": {
        "level": "INFO",
        "timestamps": True,
    },
}


def default_config() -> dict[str, Any]:
    """Return a fresh, deep copy of :data:`DEFAULTS`."""
    return copy.deepcopy(DEFAULTS)


def load_config(path: str | os.PathLike[str] | None = None) -> dict[str, Any]:
    """Build the effective configuration as a nested dict.

    ``path`` names an optional TOML file. Raises :class:`ConfigError` for a
    missing or malformed file, an unknown setting in the file, or a value of
    the wrong type.
    """
    config = default_config()
    if path is not None:
        source = os.fspath(path)
        _merge(config, _read_file(source), source, prefix="")
    return config


def _read_file(source: str) -> dict[str, Any]:
    try:
        with open(source, "rb") as fh:
            return tomllib.load(fh)
    except FileNotFoundError:
        raise ConfigError(f"config file not found: {source}") from None
    except tomllib.TOMLDecodeError as exc:
        raise ConfigError(f"{source}: invalid TOML: {exc}") from None


def _merge(target: dict[str, Any], data: Mapping[str, Any], source: str, prefix: str) -> None:
    """Copy file values from ``data`` into ``target``, checking names and types."""
    for key, value in data.items():
        dotted = f"{prefix}{key}"
        if key not in target:
            raise ConfigError(f"{source}: unknown setting {dotted!r}")
        current = target[key]
        if isinstance(current, dict):
            if not isinstance(value, dict):
                raise ConfigError(f"{source}: {dotted!r} must be a table")
            _merge(current, value, source, dotted + ".")
        else:
            target[key] = _check_type(value, current, f"{source}: {dotted!r}")


def _check_type(value: Any, default: Any, where: str) -> Any:
    """Return ``value`` if it has the type of ``default``, else raise."""
    if isinstance(default, bool):
        if isinstance(value, bool):
            return value
    elif isinstance(default, int):
        if isinstance(value, int) and not isinstance(value, bool):
            return value
    elif isinstance(default, float):
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            return float(value)
    elif isinstance(default, list):
        if isinstance(value, list) and all(isinstance(item, str) for item in value):
            return list(value)
    elif isinstance(default, str):
        if isinstance(value, str):
            return value
    raise ConfigError(f"{where} must be {_type_name(default)}, got {value!r}")


def _type_name(default: Any) -> str:
    if isinstance(default, bool):
        return "a boolean"
    if isinstance(default, int):
        return "an integer"
    if isinstance(default, float):
        return "a number"
    if isinstance(default, list):
        return "a list of strings"
    return "a string"
