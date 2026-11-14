// Rule 2: the business day is the calendar date of the instant in the
// store's time zone, daylight saving time included.
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { acme } from './_workspace.mjs';

const { businessDay, createCatalog, importOrdersJson, priceOrder } = acme;
const NY = 'America/New_York';
const KOLKATA = 'Asia/Kolkata';

describe('business day', () => {
  test('business_day_uses_store_zone_for_offset_timestamps', () => {
    assert.equal(businessDay('2026-04-01T00:30:00-04:00', NY), '2026-04-01');
    assert.equal(businessDay('2026-03-31T23:30:00-04:00', NY), '2026-03-31');
    assert.equal(businessDay('2026-03-31T23:50:00+05:30', KOLKATA), '2026-03-31');
    assert.equal(businessDay('2026-04-01T00:10:00+05:30', KOLKATA), '2026-04-01');
    assert.equal(businessDay('2026-04-01T05:20:00+05:30', 'UTC'), '2026-03-31');
    assert.equal(businessDay('2026-04-01T05:40:00+05:30', 'UTC'), '2026-04-01');
  });

  test('same_instant_written_with_different_offsets', () => {
    const sameInstant = ['2026-04-01T03:30:00Z', '2026-03-31T23:30:00-04:00', '2026-04-01T09:00:00+05:30', '2026-03-31T20:30:00-07:00'];
    for (const ts of sameInstant) {
      assert.equal(businessDay(ts, NY), '2026-03-31', `${ts} in New York`);
      assert.equal(businessDay(ts, KOLKATA), '2026-04-01', `${ts} in Kolkata`);
      assert.equal(businessDay(ts, 'UTC'), '2026-04-01', `${ts} in UTC`);
    }
  });

  test('daylight_saving_time_is_respected', () => {
    assert.equal(businessDay('2026-07-16T00:30:00-04:00', NY), '2026-07-16');
    assert.equal(businessDay('2026-07-16T04:30:00Z', NY), '2026-07-16');
    assert.equal(businessDay('2026-07-16T03:30:00Z', NY), '2026-07-15');
    assert.equal(businessDay('2026-03-08T01:30:00-05:00', NY), '2026-03-08');
    assert.equal(businessDay('2026-03-08T23:30:00-04:00', NY), '2026-03-08');
    assert.equal(businessDay('2026-11-01T23:30:00-08:00', 'America/Los_Angeles'), '2026-11-01');
    assert.equal(businessDay('2026-01-15T00:30:00+13:00', 'Pacific/Auckland'), '2026-01-15');
    assert.equal(businessDay('2026-01-15T00:30:00+13:00', 'UTC'), '2026-01-14');
  });

  test('priced_order_reports_business_day', () => {
    const catalog = createCatalog([{ sku: 'TEA-01', name: 'Green tea', price: '4.50' }]);
    const [order] = importOrdersJson(
      JSON.stringify([{ id: 'W-9', placedAt: '2026-04-01T00:05:00-04:00', lines: [{ sku: 'TEA-01', qty: 1 }] }]),
    );
    assert.equal(priceOrder(order, { catalog, store: { timeZone: NY } }).businessDay, '2026-04-01');
    assert.equal(priceOrder(order, { catalog, store: { timeZone: 'America/Los_Angeles' } }).businessDay, '2026-03-31');
    const raw = { id: 'W-10', placedAt: '2026-03-31T21:30:00-07:00', lines: [{ sku: 'TEA-01', qty: 1 }] };
    assert.equal(priceOrder(raw, { catalog, store: { timeZone: NY } }).businessDay, '2026-04-01');
  });
});
