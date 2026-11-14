// Rule 1 (a timestamp names one instant) and rule 5 (malformed input is
// still rejected).
import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { acme } from './_workspace.mjs';

const { parseTimestamp, importOrdersCsv, importOrdersJson, ImportError } = acme;
const HEADER = 'order_id,placed_at,customer,sku,qty,coupon\n';

describe('timestamps', () => {
  test('offset_timestamps_name_one_instant', () => {
    const cases = [
      ['2026-04-01T00:30:00-04:00', '2026-04-01T04:30:00.000Z'],
      ['2026-03-31T23:50:00+05:30', '2026-03-31T18:20:00.000Z'],
      ['2026-01-15T00:30+13:00', '2026-01-14T11:30:00.000Z'],
      ['2026-07-16T00:30:00.250-07:00', '2026-07-16T07:30:00.250Z'],
      ['2026-11-01T23:45:00-09:30', '2026-11-02T09:15:00.000Z'],
      ['2026-04-01T03:30:00+00:00', '2026-04-01T03:30:00.000Z'],
      ['2026-04-01T03:30:00Z', '2026-04-01T03:30:00.000Z'],
    ];
    for (const [text, iso] of cases) assert.equal(parseTimestamp(text).toISOString(), iso, text);
  });

  test('importers_return_the_instant', () => {
    const csv = importOrdersCsv(
      `${HEADER}P-1,2026-04-01T00:30:00-04:00,ana,TEA-01,1,\nP-2,2026-04-01T05:20:00+05:30,raj,TEA-01,2,\nP-1,,,MUG-01,1,\n`,
    );
    assert.deepEqual(csv.map((o) => o.placedAt.toISOString()), ['2026-04-01T04:30:00.000Z', '2026-03-31T23:50:00.000Z']);
    const json = importOrdersJson(
      JSON.stringify([{ id: 'W-1', placedAt: '2026-03-31T22:10:00-07:00', lines: [{ sku: 'TEA-01', qty: 1 }] }]),
    );
    assert.equal(json[0].placedAt.toISOString(), '2026-04-01T05:10:00.000Z');
  });

  test('malformed_timestamps_still_rejected', () => {
    for (const bad of [
      '2026-04-01T10:00:00+5:30',
      '2026-04-01T10:00:00+0530',
      '2026-04-01T10:00:00',
      '2026-04-01 10:00:00Z',
      '2026-04-31T10:00:00Z',
      '2026-04-01T10:61:00Z',
    ]) {
      assert.throws(() => parseTimestamp(bad), RangeError, bad);
    }
    assert.throws(
      () => importOrdersCsv(`${HEADER}P-1,2026-04-01T00:30:00-4:00,ana,TEA-01,1,\n`),
      (err) => err instanceof ImportError && /^line 2:/.test(err.message),
    );
  });
});
