Invoice INV-2041 is for EUR 1,250.00. The customer wants the amount quoted
in US dollars and Japanese yen at today's rates.

Get the rates from our fx API. Its URL is under the key `fx` in the JSON in
the environment variable `BENCH_EXTERNAL_APIS`, and `API.md` in the
workspace describes it. Do not use rates from memory.

Write `quote.json` in the workspace:

```json
{"invoice": "INV-2041", "eur": 1250.00, "usd": <amount in USD, 2 decimals>, "jpy": <amount in JPY, whole yen>, "rate_date": "<the date the API gives for the rates>"}
```
