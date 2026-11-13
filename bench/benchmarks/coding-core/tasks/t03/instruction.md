# Accept day-first dates in the CSV importer

Some of our customers send line-item files exported from spreadsheets that write dates as `07/03/2026` (day first). The importer (`ledger.load_invoices`, `ledger.read_invoices`, and therefore `python -m ledger ...`) rejects them today. Make it accept them.

## Accepted formats

The `date` column must accept exactly these two formats:

1. `YYYY-MM-DD`, as today, for example `2026-03-07`.
2. Day first with slashes: `D/M/YYYY`, where the day and the month are each written with one or two digits and the year with exactly four digits. `7/3/2026`, `07/3/2026` and `07/03/2026` all mean 7 March 2026.

Slash dates are always read day first, even when both numbers are 12 or less: `03/04/2026` is 3 April 2026. Spaces before or after the date are ignored. The imported date (`Invoice.issued`) is a `datetime.date`, and the export keeps writing dates as `YYYY-MM-DD` whichever format the input used.

## Rejected values

A rejected date raises `ledger.CsvFormatError` (so the CLI exits with status 1). The message starts with `line N:`, where N is the line number of the row in the file (the header row is line 1), and contains the date exactly as it appears in the file.

- A slash date with a two-digit year, such as `07/03/26`, is ambiguous because the century is unknown. Its message contains the word `ambiguous`.
- Every other date that is not in one of the two formats, or that names a day that does not exist, is invalid, and its message contains `invalid date`. This includes `31/04/2026` and `29/02/2025` (no such day), `03/15/2026` (there is no month 15; slash dates are never read month first), `2026/03/07`, `07-03-2026`, `7.3.2026` and `2026-3-7`.

## Rows of the same invoice

The date of an invoice still comes from its first row, and a later row of the same invoice may leave the date blank or give the same date. The same date written in the other format counts as the same date: `2026-03-07` on one row and `07/03/2026` on a later row of the same invoice are accepted. A later row with a different date is still rejected with `CsvFormatError`, reported on that row's line.
