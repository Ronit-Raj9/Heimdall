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
    parent = list(range(len(records)))

    def root(i: int) -> int:
        while parent[i] != i:
            parent[i] = parent[parent[i]]
            i = parent[i]
        return i

    first_seen: dict[tuple[str, str], int] = {}
    for index, record in enumerate(records):
        for field in fields:
            value = normalise_key(record.get(field))
            if not value:
                continue
            earlier = first_seen.setdefault((field, value), index)
            if earlier != index:
                a, b = root(earlier), root(index)
                if a != b:
                    # Keep the smaller index as the root of the merged group.
                    parent[max(a, b)] = min(a, b)

    groups: dict[int, list[int]] = {}
    for index in range(len(records)):
        groups.setdefault(root(index), []).append(index)
    found = [members for members in groups.values() if len(members) > 1]
    found.sort(key=lambda members: members[0])
    return found


def drop_duplicates(records: Sequence[Mapping[str, Any]], keys: Iterable[str]) -> list[Mapping[str, Any]]:
    """Return ``records`` without duplicates, in their original order.

    The first record (lowest index) of each duplicate group is kept. The
    input is not modified.
    """
    dropped: set[int] = set()
    for group in find_duplicates(records, keys):
        dropped.update(group[1:])
    return [record for index, record in enumerate(records) if index not in dropped]


def _check_keys(keys: Iterable[str]) -> list[str]:
    fields = list(keys)
    if not fields:
        raise ValueError("at least one key field is required")
    if isinstance(keys, str) or not all(isinstance(field, str) for field in fields):
        raise TypeError("keys must be a list of field names")
    return fields
