import { test } from 'node:test';
import assert from 'node:assert/strict';
import { businessDay, parseTimestamp } from '../src/index.js';

test('parseTimestamp reads UTC timestamps', () => {
  assert.equal(parseTimestamp('2026-03-12T14:05:00Z').toISOString(), '2026-03-12T14:05:00.000Z');
  assert.equal(parseTimestamp('2026-03-12T14:05Z').toISOString(), '2026-03-12T14:05:00.000Z');
  assert.equal(parseTimestamp('2026-03-12T14:05:09.5Z').toISOString(), '2026-03-12T14:05:09.500Z');
});

test('parseTimestamp rejects malformed input', () => {
  for (const bad of ['2026-03-12', '2026-03-12T14:05:00', '2026-02-30T10:00:00Z', '2026-03-12T24:00:00Z', 'soon']) {
    assert.throws(() => parseTimestamp(bad), RangeError, bad);
  }
});

test('businessDay is the calendar date in the store time zone', () => {
  assert.equal(businessDay('2026-03-12T14:05:00Z', 'America/New_York'), '2026-03-12');
  assert.equal(businessDay('2026-03-10T02:00:00Z', 'America/New_York'), '2026-03-09');
  assert.equal(businessDay('2026-03-10T02:00:00Z'), '2026-03-10');
  assert.throws(() => businessDay('2026-03-10T02:00:00Z', 'Mars/Olympus'), RangeError);
});
