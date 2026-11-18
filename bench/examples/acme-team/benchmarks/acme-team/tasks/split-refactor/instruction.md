Replace the deprecated `legacy_round` helper with `acme.money.round_money`
in `acme/prices.py` and `acme/stock.py`. Both modules must import
`round_money` from `acme.money` and keep their behaviour otherwise. Do not
change `acme/legacy.py`; other callers still use it.

The lead splits the work: one worker takes `acme/prices.py`, the other
`acme/stock.py`, and the reviewer checks that no module still uses
`legacy_round` before the lead reports done.
