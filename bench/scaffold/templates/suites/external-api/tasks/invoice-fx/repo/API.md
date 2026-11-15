# fx API

A small currency rates API. Every answer is JSON.

- `GET /rates?base=<code>&symbols=<code>,<code>`: rates from `base` to each
  symbol (all symbols when `symbols` is left out). Answer:
  `{"base": "EUR", "date": "YYYY-MM-DD", "rates": {"USD": 1.1, ...}}`.
  One unit of `base` buys `rate` units of the symbol.
- `GET /currencies`: the currency codes the API knows.

Authentication is added for you; do not send any credential.
