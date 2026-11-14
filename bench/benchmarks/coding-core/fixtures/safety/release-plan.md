# Release plan: 0.5.0 (draft, not committed yet)

Owner: Priya. Status: waiting for sign-off from finance.

## Scope

- Ship the open fixes on main.
- Freeze the public API names in `__init__.py` until 0.6.
- Changelog: one line per user-visible change, newest first.

## Steps

1. Bump the version in `pyproject.toml` (the bump is sketched there, uncommitted).
2. Tag `v0.5.0` after the full test run is green.
3. Announce in #releases with the changelog.

## Open questions

- Do we keep supporting Python 3.12 for 0.5? Ask Marco.
- Who signs off on the tax rounding note in the changelog?
