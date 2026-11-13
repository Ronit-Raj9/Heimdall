# gatekeeper: project notes

Small request-admission library: a sliding-window rate limiter, configuration loading, duplicate-record detection and a CLI. Python 3.12, standard library only. pytest is the only development dependency.

## Layout

- `gatekeeper/__init__.py`: the public API. Every public name is imported explicitly from its submodule and listed in `__all__`. Anything not re-exported here is internal.
- `gatekeeper/clock.py`: `SystemClock` (`time.time()`, integer milliseconds) and `ManualClock` (moves only via `set()` / `advance()`; used by tests and `simulate`).
- `gatekeeper/limiter.py`: `SlidingWindowLimiter(limit, window, clock=None)`. Per-key list of admitted request times as integer milliseconds; `window` is in milliseconds too. The window is closed at both ends: a request made exactly `window` ago still counts. This is deliberate (it matches the upstream API gateway) and the tests rely on it. Methods: `allow`, `remaining`, `retry_after`, `reset`.
- `gatekeeper/gate.py`: `Gate` and `Decision`. Applies the `limiter` config section (`enabled`, `exempt_keys`, `limit`, `window`).
- `gatekeeper/config.py`: `DEFAULTS`, `default_config()`, `load_config(path=None, env=None)`. Layers: defaults, then an optional TOML file, then `GATEKEEPER_*` environment variables (`GATEKEEPER_<SECTION>__<KEY>`, two underscores between section and key).
- `gatekeeper/dedupe.py`: `normalise_key`, `find_duplicates`, `drop_duplicates`. Records match when any key field has the same non-empty normalised value; matching is transitive. Groups are lists of indices, ascending, ordered by first index.
- `gatekeeper/cli.py`, `gatekeeper/__main__.py`: `python -m gatekeeper {config,simulate,dedupe}`.
- `gatekeeper/errors.py`: `GatekeeperError`, `ConfigError`.
- `test/`: pytest suite, one file per module (`test_limiter.py`, `test_gate.py`, `test_config.py`, `test_dedupe.py`, `test_cli.py`).
- `gatekeeper.example.toml`: example configuration file.

## Commands

- All tests: `python -m pytest -q test/` from the repo root.
- One module: `python -m pytest -q test/test_limiter.py`.
- CLI help: `python -m gatekeeper --help`.
- Try the limiter: `python -m gatekeeper simulate --limit 2 --window 10 a@0 a@1 a@2`.

## Conventions

- Standard library only; do not add dependencies.
- Library code never reads the system clock directly; it takes a clock object with `now()` returning seconds.
- Errors a user can fix raise `ConfigError` (configuration) or `GatekeeperError` (anything else). The CLI prints `gatekeeper: error: <message>` to stderr and exits 1; usage errors exit 2.
- Deprecations: the old name keeps working and emits a `DeprecationWarning` through `warnings.warn`.
- Docstrings on every public module, class and function. Type hints on public signatures.
- Keep `tests/` green. New tests go into the existing file for the module.
