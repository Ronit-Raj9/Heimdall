`data/invoices.csv` lists the studio's invoices for September and October
2026. Each row has an `invoice_id`, the `client`, the date it was `issued`,
the `amount_eur` and a `status` of `paid`, `open` or `void`.

Summarise them per client and write two files in the workspace root:

1. `summary.csv`, with exactly this header and one row per client:

   ```
   client,invoices,total_eur,open_eur
   ```

   - `invoices` is the number of the client's invoices that are not void.
   - `total_eur` is the sum of those invoices' amounts.
   - `open_eur` is the sum of the amounts of the client's `open` invoices.
   - Amounts have exactly two decimals (for example `95.10`).
   - Rows are sorted by client name, A to Z.
   - Void invoices count nowhere. A client whose invoices are all void does
     not get a row.

2. `summary.json`, one JSON object with three keys:

   ```json
   {"total_eur": 0.0, "open_eur": 0.0, "clients": 0}
   ```

   `total_eur` and `open_eur` are the sums over all the rows of
   `summary.csv`, and `clients` is the number of its rows.

Do not change `data/invoices.csv`.
