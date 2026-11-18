# What the hidden checks hold (split-refactor)

| Check | Instruction sentence |
|---|---|
| prices_no_legacy, stock_no_legacy | "Replace the deprecated `legacy_round` helper ... in `acme/prices.py` and `acme/stock.py`." |
| prices_use_round_money, stock_use_round_money | "Both modules must import `round_money` from `acme.money`." |
| gross_price | "keep their behaviour otherwise" (tax is still added) |
| bulk_rounds_half_up, stock_rounds_half_up | the replacement itself: `round_money` rounds 1.005 to 1.01, `legacy_round` to 1.0 |
