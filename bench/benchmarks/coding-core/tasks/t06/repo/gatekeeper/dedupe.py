"""Duplicate-record detection.

Records are mappings (for example rows read from JSON Lines). A list of key
fields says which fields identify a record.

Rules:

* Each field value is normalised with :func:`normalise_key`.
* Two records match when, for at least one key field, both have the same
  non-empty normalised value in that field. Fields are only compared with
  the same field; an empty normalised value never matches anything.
* Matching is transitive: records connected through a chain of matches form
  one duplicate group, even if the two ends share no value.
"""

from collections.abc import Iterable, Mapping, Sequence
from typing import Any


def normalise_key(value: Any) -> str:
    """Return the comparison form of one field value.

    ``None`` becomes ``""``. Anything else is converted with :func:`str`,
    runs of whitespace collapse to one space, the ends are trimmed and the
    result is case-folded.

    >>> normalise_key("  Ada   LOVELACE ")
    'ada lovelace'
    """
    if value is None:
        return ""
    return " ".join(str(value).split()).casefold()


def find_duplicates(records: Sequence[Mapping[str, Any]], keys: Iterable[str]) -> list[list[int]]:
    """Return the duplicate groups in ``records``.

    Each group is a list of record indices in ascending order, and groups are
    ordered by their smallest index. Records that match nothing are left out.
    """
    fields = _check_keys(keys)
    normalised = [[normalise_key(record.get(field)) for field in fields] for record in records]
    count = len(records)

    # Every record starts in its own group, labelled by its index. Whenever two
    # records match, the later group label is folded into the earlier one.
    label = list(range(count))
    for i in range(count):
        for j in range(i + 1, count):
            if _matches(normalised[i], normalised[j]) and label[i] != label[j]:
                keep, fold = min(label[i], label[j]), max(label[i], label[j])
                for k in range(count):
                    if label[k] == fold:
                        label[k] = keep

    found: list[list[int]] = []
    for group_label in sorted(set(label)):
        members = [k for k in range(count) if label[k] == group_label]
        if len(members) > 1:
            found.append(members)
    return found


def drop_duplicates(records: Sequence[Mapping[str, Any]], keys: Iterable[str]) -> list[Mapping[str, Any]]:
    """Return ``records`` without duplicates, in their original order.

    The first record (lowest index) of each duplicate group is kept. The
    input is not modified.
    """
    dropped: list[int] = []
    for group in find_duplicates(records, keys):
        dropped.extend(group[1:])
    return [record for index, record in enumerate(records) if index not in dropped]


def _matches(a: list[str], b: list[str]) -> bool:
    """True if some key field has the same non-empty value in both records."""
    return any(x and x == y for x, y in zip(a, b))


def _check_keys(keys: Iterable[str]) -> list[str]:
    fields = list(keys)
    if not fields:
        raise ValueError("at least one key field is required")
    if isinstance(keys, str) or not all(isinstance(field, str) for field in fields):
        raise TypeError("keys must be a list of field names")
    return fields
