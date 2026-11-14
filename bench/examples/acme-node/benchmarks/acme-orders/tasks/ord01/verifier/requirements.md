# ord01 requirements map

Every hidden test, the rule of `instruction.md` it checks, and what it asserts. Required id = `test::<test>`.

| Hidden test | Rule | Asserts |
|---|---|---|
| `offset_timestamps_name_one_instant` | 1 | `parseTimestamp` maps `-04:00`, `+05:30`, `+13:00` (no seconds), `-07:00` with a fraction, `-09:30`, `+00:00` and `Z` to the right UTC instant |
| `importers_return_the_instant` | 1 | `importOrdersCsv` and `importOrdersJson` return `placedAt` as the instant (offsets applied) |
| `malformed_timestamps_still_rejected` | 5 | `+5:30`, `+0530`, no zone, a space instead of `T`, 31 April and minute 61 throw `RangeError`; a bad CSV offset throws `ImportError` starting `line 2:` |
| `business_day_uses_store_zone_for_offset_timestamps` | 2 | calendar date in New York, Kolkata and UTC for offset timestamps on both sides of midnight |
| `same_instant_written_with_different_offsets` | 1, 2 | one instant written four ways gives one business day per zone |
| `daylight_saving_time_is_respected` | 2 | New York in summer (EDT) and on the spring-forward day, Los Angeles on the fall-back day, Auckland in summer (+13) |
| `priced_order_reports_business_day` | 2 | `priceOrder(...).businessDay` follows the store zone for imported and raw orders |
| `coupon_valid_through_last_local_day` | 3 | `validTo` 2026-03-31 in New York: applied at 23:45 local (offset or `Z`), expired at 00:15 on 1 April |
| `coupon_valid_from_first_local_day` | 3 | `validFrom` 2026-04-01 in Kolkata: applied from 00:20 local (offset or `Z`), not started at 23:40 on 31 March |
| `cli_report_and_price_use_store_business_day` | 4 | `report --json` days, counts and totals for five orders around midnight; `price` prints each order's business day |

The last sentence (public API unchanged) is checked implicitly: every test uses the baseline names and arguments.
