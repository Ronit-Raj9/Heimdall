"""Date parsing for the CSV importer."""

from __future__ import annotations

import re
from datetime import date

_ISO_RE = re.compile(r"([0-9]{4})-([0-9]{2})-([0-9]{2})")


def parse_date(text: str) -> date:
    """Parse an invoice date written as ``YYYY-MM-DD``.

    Surrounding whitespace is ignored. Anything else, including a day that
    does not exist such as ``2026-02-30``, raises ValueError naming the text
    as written.
    """
    value = text.strip()
    match = _ISO_RE.fullmatch(value)
    if match is None:
        raise ValueError(f"invalid date {text!r}")
    year, month, day = (int(group) for group in match.groups())
    try:
        return date(year, month, day)
    except ValueError:
        raise ValueError(f"invalid date {text!r}") from None
