# ord03 requirements map

Every hidden test, the rule of `instruction.md` it checks, and what it asserts. Required id = `test::<test>`. Warning tests run each case in a new Node process, so "once per process" is checked exactly.

| Hidden test | Rule | Asserts |
|---|---|---|
| `options_form_prices_like_positional` | 1, 2 | options and positional forms (with and without store, single and batch) give deep-equal results and the same `OrderError` for an unknown SKU |
| `options_form_requires_catalog` | 1 | no options, no catalog, or a catalog without `get` throws `TypeError` |
| `format_cents_replaces_format_money` | 3 | `formatCents` and `formatMoney` give the baseline output; `formatCents(1.5)` throws `TypeError` |
| `positional_forms_warn_once_with_code` | 4 | six positional calls emit exactly one `DeprecationWarning`, code `ACME_DEP001`, message contains `{ catalog, store }` |
| `priceorders_positional_warns_first` | 4 | a positional `priceOrders` call warns first, and a later positional `priceOrder` does not warn again (shared code) |
| `format_money_warns_once_independently` | 4 | repeated `formatMoney` calls warn once with `ACME_DEP002` (message contains `formatCents`), and `ACME_DEP001` still warns once |
| `new_forms_do_not_warn` | 4, 5 | options forms, `formatCents`, the importers and `parseStore` emit nothing and print nothing to stderr |
| `no_deprecation_flag_silences_warnings` | 4 | with `--no-deprecation` the old forms work and nothing is emitted or printed |
| `throw_deprecation_flag_makes_warnings_fatal` | 4 | with `--throw-deprecation` each deprecated form ends the process with its `DeprecationWarning` and code; the new forms run fine |
| `cli_uses_no_deprecated_api` | 5 | `price` and `report` exit 0 with empty stderr, with and without `--throw-deprecation` |
