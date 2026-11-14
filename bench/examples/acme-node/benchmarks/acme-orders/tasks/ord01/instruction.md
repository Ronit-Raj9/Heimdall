# Orders near midnight land on the wrong business day

Store managers report that the daily report and coupon checks get the day wrong for orders placed around midnight:

- Brooklyn (store time zone `America/New_York`): an order rung up at 00:30 on 1 April shows up in the report for 31 March, and the `SPRING10` coupon, valid through 31 March, was accepted for an order rung up at 00:15 on 1 April.
- Mumbai (store time zone `Asia/Kolkata`): orders rung up in the evening show up in the next day's report.

The point-of-sale export writes timestamps with the store's own UTC offset (for example `2026-04-01T00:30:00-04:00`); the web shop writes UTC (`2026-04-01T04:30:00Z`). Web-shop orders look right.

Make the `@acme/orders` package follow these rules:

1. A `placedAt` timestamp is ISO 8601: `YYYY-MM-DDTHH:MM`, optionally followed by `:SS` and then optionally by a fraction of up to three digits (`.250`), then either `Z` or a UTC offset written `+HH:MM` or `-HH:MM`. It names exactly one instant: the written clock reading minus the offset is the UTC time. `2026-04-01T00:30:00-04:00` and `2026-04-01T04:30:00Z` are the same instant. `parseTimestamp` returns that instant as a `Date`, and so do the `placedAt` values of the orders returned by `importOrdersCsv` and `importOrdersJson`.
2. The business day of an order is the calendar date, written `YYYY-MM-DD`, of that instant in the store's time zone (`store.timeZone`, an IANA name such as `America/New_York`), with daylight saving time taken into account. This is what `businessDay(placedAt, timeZone)` returns and what a priced order reports as `businessDay`.
3. Coupon windows (`validFrom`, `validTo`) are business days, and both ends are included.
4. `acme-orders report` groups orders by business day, and `acme-orders price` prints each order's business day.
5. Input that is not a timestamp of the form above is still refused as it is today: `parseTimestamp` throws a `RangeError`, and the importers throw an `ImportError` that names the CSV line or the order.

Keep the public API (`src/index.js`) as it is: same names, same arguments. The existing tests in `test/` must keep passing unchanged; you may add new test files.
