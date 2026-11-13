# t08 hidden tests: requirement map

All tests are in `verifier/tests/test_rename_shim.py`. Numbers refer to the numbered list in `instruction.md`.

| Test | Instruction sentence it checks |
|---|---|
| `test_new_name_is_public` | 2: importable from `gatekeeper` and `gatekeeper.dedupe` as the same function object, listed in `gatekeeper.__all__`. |
| `test_new_name_returns_tuples_and_takes_keys_by_keyword_only` | 1: same groups and order as `find_duplicates`, a list of tuples, `keys` keyword-only (`TypeError` when positional); 6: calling `duplicate_groups` emits no `DeprecationWarning`. |
| `test_old_name_still_public_and_unchanged` | 3: positional or keyword `records`/`keys`, same list of lists, same errors (`ValueError` for empty `keys`), importable from both modules as the same object, still in `__all__`. |
| `test_every_call_to_old_name_warns_once_with_exact_message` | 4: "Every call to `find_duplicates` emits exactly one `DeprecationWarning` whose message is exactly ...". |
| `test_warning_points_at_the_caller` | 5: "its file name and line number are those of the calling line, not a location inside gatekeeper." |
| `test_rest_of_the_api_does_not_warn` | 6: `drop_duplicates` and `duplicate_groups` emit no `DeprecationWarning`. |
| `test_import_and_cli_do_not_warn_and_cli_output_is_unchanged` | 6: `import gatekeeper` / `from gatekeeper import find_duplicates` and `python -m gatekeeper dedupe` succeed under `-W error::DeprecationWarning`; 7: `dedupe` output unchanged (`[[0, 2], [1, 3, 5]]` plus newline). |
