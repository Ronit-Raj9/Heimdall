# Rename `find_duplicates` to `duplicate_groups`, keeping the old name as a deprecated alias

`gatekeeper.find_duplicates(records, keys)` is being replaced by a new public function, `duplicate_groups`. Existing callers need a release to migrate, so the old name must keep working while it tells them to move.

1. Add `duplicate_groups(records, *, keys)`. `keys` is keyword-only: passing it positionally raises `TypeError`. It finds exactly the groups `find_duplicates` finds today, in the same order, but returns a list of tuples: each group is a tuple of record indices instead of a list.
2. `duplicate_groups` is part of the public API: it can be imported from `gatekeeper` and from `gatekeeper.dedupe` (the same function object), and its name is listed in `gatekeeper.__all__`.
3. `find_duplicates` keeps working exactly as before: `records` and `keys` may still be passed positionally or by keyword, it returns the same list of lists, and it raises the same errors. It stays importable from `gatekeeper` and from `gatekeeper.dedupe` (the same function object) and stays listed in `gatekeeper.__all__`.
4. Every call to `find_duplicates` emits exactly one `DeprecationWarning` whose message is exactly `find_duplicates() is deprecated; use duplicate_groups() instead`.
5. That warning is attributed to the code that called `find_duplicates`: its file name and line number are those of the calling line, not a location inside gatekeeper.
6. Nothing else emits a `DeprecationWarning`: importing `gatekeeper` (including `from gatekeeper import find_duplicates`), calling `duplicate_groups` or `drop_duplicates`, and running `python -m gatekeeper dedupe` all work with warnings turned into errors (`python -W error::DeprecationWarning`).
7. The output of `python -m gatekeeper dedupe` does not change.

The existing tests must keep passing.
