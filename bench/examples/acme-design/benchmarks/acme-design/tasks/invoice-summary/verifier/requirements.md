# invoice-summary: what each check holds, and why

The instruction asks for `summary.csv` (per client) and `summary.json`
(totals) from `data/invoices.csv`. `tests/verify.py` holds the expected
numbers, worked out once from that file, and reads only the exported
artifacts:

| Check | Source |
|---|---|
| `csv` | The header `client,invoices,total_eur,open_eur`; one row per client sorted A to Z; void invoices count nowhere and Tidewater Florist (only a void invoice) gets no row; counts exact, amounts within 0.005 and written with two decimals |
| `json` | `total_eur` 10937.60 and `open_eur` 5940.85 (within 0.005) and `clients` 4, the sums and the row count of the CSV |

Expected rows: Birchwood Bakery 2, 1625.25, 375.25; Harbor Lane Books 3,
2075.60, 1120.00; Marlow Cycle Works 2, 2880.40, 730.00; Quillfeather Studio
3, 4356.35, 3715.60.

The mutants: counting void invoices (wrong counts and totals, an extra
Tidewater Florist row), and an `open_eur` that repeats the total.
