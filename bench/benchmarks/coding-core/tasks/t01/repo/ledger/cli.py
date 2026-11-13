"""Command line interface: ``python -m ledger COMMAND FILE``.

Commands:

``summary FILE``
    One line per invoice with its total, then a grand total.
``export FILE [-o PATH]``
    Every invoice with its lines and totals, as JSON.

Exit status is 0 on success, 1 when the file cannot be read or imported and
2 for usage errors.
"""

from __future__ import annotations

import argparse
import sys
from collections.abc import Sequence
from typing import IO

from . import __version__
from .csvio import load_invoices
from .errors import LedgerError
from .export import export_json
from .invoice import Invoice
from .money import format_cents


def build_parser() -> argparse.ArgumentParser:
    """The argument parser for ``python -m ledger``."""
    parser = argparse.ArgumentParser(prog="ledger", description="Invoice totals from a CSV of line items.")
    parser.add_argument("--version", action="version", version=f"ledger {__version__}")
    commands = parser.add_subparsers(dest="command", required=True, metavar="COMMAND")

    summary = commands.add_parser("summary", help="print one line per invoice and a grand total")
    summary.add_argument("file", help="CSV file of line items")

    export = commands.add_parser("export", help="write invoices with lines and totals as JSON")
    export.add_argument("file", help="CSV file of line items")
    export.add_argument("-o", "--output", metavar="PATH", help="write to PATH instead of standard output")
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    """Run the CLI and return the exit status."""
    args = build_parser().parse_args(argv)
    try:
        invoices = load_invoices(args.file)
    except OSError as exc:
        print(f"error: cannot read {args.file}: {exc.strerror}", file=sys.stderr)
        return 1
    except LedgerError as exc:
        print(f"error: {args.file}: {exc}", file=sys.stderr)
        return 1

    if args.command == "summary":
        print_summary(invoices, sys.stdout)
    elif args.command == "export":
        _export(invoices, args)
    return 0


def print_summary(invoices: Sequence[Invoice], out: IO[str]) -> None:
    """Write one line per invoice and a grand total to ``out``."""
    grand_total = 0
    for invoice in invoices:
        total = invoice.totals().total
        grand_total += total
        out.write(
            f"{invoice.number:<12}{invoice.issued.isoformat()}  "
            f"{invoice.customer:<24.24}{format_cents(total):>12}\n"
        )
    out.write(f"{'TOTAL':<12}{'':<10}  {'':<24}{format_cents(grand_total):>12}\n")
    out.write(f"{len(invoices)} invoice(s)\n")


def _export(invoices: Sequence[Invoice], args: argparse.Namespace) -> None:
    if args.output:
        with open(args.output, "w", encoding="utf-8", newline="") as fh:
            export_json(invoices, fh)
    else:
        export_json(invoices, sys.stdout)
