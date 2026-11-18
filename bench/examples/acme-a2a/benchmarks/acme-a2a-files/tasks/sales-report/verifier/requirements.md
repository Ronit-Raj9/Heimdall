# sales-report: what each part of the verifier holds, and why

The instruction asks for two files from `data/sales.csv`. `tests/verify.py`
reads them at `/grader/artifacts/report/report.csv` and
`/grader/artifacts/chart/chart.svg` and reports one dimension per file.

| Dimension | Holds | Source |
|---|---|---|
| `report` | Header `region,orders,units,revenue`; one row per region in region order; orders and units exact; revenue within half a cent | Step 1: completed orders only, revenue is units times unit price |
| `chart` | Well-formed SVG; one `<rect>` per region with a `<title>` naming it; heights in proportion to revenue within 2% of the tallest bar | Step 2: one titled bar per region, height proportional to revenue |

Expected values (completed orders only): East 2 orders, 8 units, 267.00;
North 3, 13, 335.97; South 3, 10, 352.46; West 3, 19, 402.43. Counting the
refunded orders S-1004 and S-1011 instead gives North 585.97 and East 326.97,
which is what the `refunds-counted` mutant writes.
