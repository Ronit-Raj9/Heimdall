# invoice-fx: what each check holds, and why

| Check | Instruction |
|---|---|
| `invoice` | "Invoice INV-2041" |
| `eur` | "is for EUR 1,250.00" |
| `usd` | "quoted in US dollars", 2 decimals: 1250 x 1.0842 = 1355.25 (plus or minus 0.01) |
| `jpy` | "Japanese yen", whole yen: 1250 x 163.27 = 204087.5, rounded to 204088 (plus or minus 1) |
| `rate_date` | "the date the API gives for the rates": 2026-11-13 in the cassette |

The rates are the ones the cassette records, so a replay run has one right
answer. A live run against a real API would need a verifier that reads the
rates from the run's tool ledger instead.
