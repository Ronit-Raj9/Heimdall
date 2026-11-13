"""Configuration loading.

Settings come from three layers. Later layers win, setting by setting:

1. built-in defaults (:data:`DEFAULTS`),
2. an optional TOML file,
3. environment variables.

The variable for a setting is ``GATEKEEPER_`` plus the setting's dotted path in
upper case with ``__`` between section and key: ``limiter.window`` is
``GATEKEEPER_LIMITER__WINDOW`` and the top-level ``name`` is
``GATEKEEPER_NAME``. Variables that name no setting are ignored.

Every setting has the type of its default. File values must already have that
type (an integer is accepted where a float is expected). Environment values
are strings and are converted; see :func:`_from_env`.
"""

import copy
import os
import tomllib
from collections.abc import Iterator, Mapping
from typing import Any

from .errors import ConfigError

ENV_PREFIX = "GATEKEEPER_"
ENV_NESTING = "__"

TRUE_WORDS = frozenset({"1", "true", "yes", "on"})
FALSE_WORDS = frozenset({"0", "false", "no", "off"})

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


def load_config(path: str | os.PathLike[str] | None = None,
                env: Mapping[str, str] | None = None) -> dict[str, Any]:
    """Build the effective configuration as a nested dict.

    ``path`` names an optional TOML file. ``env`` maps variable names to
    values; ``None`` means :data:`os.environ`. Raises :class:`ConfigError` for
    a missing or malformed file, an unknown setting in the file, or a value of
    the wrong type.
    """
    config = default_config()
    if path is not None:
        source = os.fspath(path)
        _merge(config, _read_file(source), source, prefix="")
    _apply_env(config, os.environ if env is None else env)
    return config


def iter_settings(config: Mapping[str, Any], prefix: str = "") -> Iterator[tuple[str, Any]]:
    """Yield ``(dotted_path, value)`` for every leaf setting in ``config``."""
    for key, value in config.items():
        dotted = f"{prefix}{key}"
        if isinstance(value, Mapping):
            yield from iter_settings(value, dotted + ".")
        else:
            yield dotted, value


def env_var_name(dotted: str) -> str:
    """Return the environment variable that overrides setting ``dotted``."""
    return ENV_PREFIX + ENV_NESTING.join(dotted.split(".")).upper()


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


def _apply_env(config: dict[str, Any], env: Mapping[str, str]) -> None:
    """Override settings in ``config`` from environment variables."""
    for dotted, current in list(iter_settings(config)):
        name = env_var_name(dotted)
        if name not in env:
            continue
        section = config
        *parents, leaf = dotted.split(".")
        for part in parents:
            section = section[part]
        section[leaf] = _from_env(env[name], current, name)


def _from_env(raw: str, default: Any, name: str) -> Any:
    """Convert the string ``raw`` to the type of ``default``.

    Booleans accept 1/0, true/false, yes/no, on/off in any case. Lists are
    comma-separated; items are stripped and empty items dropped. Strings are
    used as given.
    """
    if isinstance(default, bool):
        word = raw.strip().lower()
        if word in TRUE_WORDS:
            return True
        if word in FALSE_WORDS:
            return False
        raise ConfigError(f"{name}: expected a boolean (1/0, true/false, yes/no, on/off), got {raw!r}")
    if isinstance(default, int):
        try:
            return int(raw.strip(), 10)
        except ValueError:
            raise ConfigError(f"{name}: expected an integer, got {raw!r}") from None
    if isinstance(default, float):
        try:
            return float(raw.strip())
        except ValueError:
            raise ConfigError(f"{name}: expected a number, got {raw!r}") from None
    if isinstance(default, list):
        return [item.strip() for item in raw.split(",") if item.strip()]
    return raw


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
