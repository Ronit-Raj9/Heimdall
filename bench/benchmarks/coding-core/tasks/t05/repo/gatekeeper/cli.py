"""Command line interface: ``python -m gatekeeper <command>``.

Commands:

``config``
    Print the effective configuration as JSON.
``simulate``
    Replay ``KEY@SECONDS`` events through a :class:`~gatekeeper.Gate` driven
    by a manual clock and print one decision per line.
``dedupe``
    Read a JSON Lines file and print its duplicate groups as a JSON array.

Errors the user can fix print ``gatekeeper: error: <message>`` to stderr and
exit with status 1. Usage errors exit with status 2.
"""

import argparse
import json
import sys
from collections.abc import Sequence
from typing import Any

from . import __version__
from .clock import ManualClock
from .config import load_config
from .dedupe import find_duplicates
from .errors import GatekeeperError
from .gate import Gate


def build_parser() -> argparse.ArgumentParser:
    """Return the argument parser for the ``gatekeeper`` command."""
    parser = argparse.ArgumentParser(prog="gatekeeper", description="Request admission helpers.")
    parser.add_argument("--version", action="version", version=f"%(prog)s {__version__}")
    commands = parser.add_subparsers(dest="command", required=True, metavar="COMMAND")

    show = commands.add_parser("config", help="print the effective configuration as JSON")
    show.add_argument("--file", "-f", help="TOML configuration file")

    simulate = commands.add_parser("simulate", help="replay KEY@SECONDS events through the limiter")
    simulate.add_argument("--file", "-f", help="TOML configuration file")
    simulate.add_argument("--limit", type=int, help="override limiter.limit")
    simulate.add_argument("--window", type=float, help="override limiter.window (seconds)")
    simulate.add_argument("events", nargs="+", metavar="KEY@SECONDS",
                          help="events in time order, e.g. alice@0 alice@2.5")

    dedupe = commands.add_parser("dedupe", help="print duplicate groups in a JSON Lines file")
    dedupe.add_argument("path", help="JSON Lines file, one object per line")
    dedupe.add_argument("--file", "-f", help="TOML configuration file")
    dedupe.add_argument("--key", action="append", dest="keys", metavar="FIELD",
                        help="key field (repeatable); default: dedupe.keys from the configuration")
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    """Run the CLI and return the process exit status."""
    args = build_parser().parse_args(argv)
    handlers = {"config": _run_config, "simulate": _run_simulate, "dedupe": _run_dedupe}
    try:
        return handlers[args.command](args)
    except GatekeeperError as exc:
        print(f"gatekeeper: error: {exc}", file=sys.stderr)
        return 1


def _run_config(args: argparse.Namespace) -> int:
    config = load_config(args.file)
    print(json.dumps(config, indent=2, sort_keys=True))
    return 0


def _run_simulate(args: argparse.Namespace) -> int:
    config = load_config(args.file)
    if args.limit is not None:
        config["limiter"]["limit"] = args.limit
    if args.window is not None:
        config["limiter"]["window"] = args.window
    clock = ManualClock()
    try:
        gate = Gate(config, clock)
    except ValueError as exc:
        raise GatekeeperError(str(exc)) from None
    for token in args.events:
        key, when = _parse_event(token)
        if when < clock.now():
            raise GatekeeperError(f"event {token!r} is earlier than the event before it")
        clock.set(when)
        decision = gate.check(key)
        verdict = "allow" if decision.allowed else "deny"
        print(f"{token} {verdict} remaining={decision.remaining}")
    return 0


def _run_dedupe(args: argparse.Namespace) -> int:
    config = load_config(args.file)
    keys = args.keys or config["dedupe"]["keys"]
    records = _read_jsonl(args.path)
    print(json.dumps(find_duplicates(records, keys)))
    return 0


def _parse_event(token: str) -> tuple[str, float]:
    key, sep, when = token.rpartition("@")
    if not sep or not key:
        raise GatekeeperError(f"bad event {token!r}: expected KEY@SECONDS")
    try:
        seconds = float(when)
    except ValueError:
        raise GatekeeperError(f"bad event {token!r}: {when!r} is not a number of seconds") from None
    if seconds < 0:
        raise GatekeeperError(f"bad event {token!r}: time must not be negative")
    return key, seconds


def _read_jsonl(path: str) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    try:
        with open(path, encoding="utf-8") as fh:
            for number, line in enumerate(fh, start=1):
                if not line.strip():
                    continue
                try:
                    record = json.loads(line)
                except json.JSONDecodeError as exc:
                    raise GatekeeperError(f"{path}:{number}: invalid JSON: {exc.msg}") from None
                if not isinstance(record, dict):
                    raise GatekeeperError(f"{path}:{number}: expected a JSON object")
                records.append(record)
    except OSError as exc:
        raise GatekeeperError(f"cannot read {path}: {exc.strerror}") from None
    return records
