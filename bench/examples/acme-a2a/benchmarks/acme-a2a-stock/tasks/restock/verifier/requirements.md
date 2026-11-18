# restock: what each check holds, and why

A map from every check in `tests/checks.py` to the instruction sentence it
checks, so a reviewer can see the checks ask for what the instruction asks
and nothing more.

| Check | Instruction |
|---|---|
| `bottle_stock_is_42` | "12 units of the 750 ml steel water bottle" arrived: 30 in stock plus 12 |
| `one_adjustment`, `adjustment_on_w100`, `adjustment_adds_12` | "Record it in the inventory": one adjustment of +12 on W-100 |
| `reason_names_delivery` | "put the delivery number in the reason" |
| `small_bottle_unchanged`, `tote_unchanged` | "Do not change any other item" |
