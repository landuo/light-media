import test from 'node:test';
import assert from 'node:assert/strict';
import { siteDataDays, siteDataDelta, siteDataBytes } from '../src/features/site-data.ts';

test('daily deltas use latest successful Shanghai day, not duplicate refreshes or failures', () => {
  const days = siteDataDays(
    [
      { stats: { captured_at: '2026-10-22T23:30:00Z', uploaded: 10, downloaded: 8 } },
      { stats: { captured_at: '2026-10-23T01:30:00Z', uploaded: 12, downloaded: 8 } },
      {
        connection: { status: 'failed' },
        stats: { captured_at: '2026-10-24T02:00:00Z', uploaded: 0 },
      },
      { stats: { captured_at: 'invalid', uploaded: 999 } },
    ],
    { captured_at: '2026-10-24T01:00:00Z', uploaded: 20, downloaded: 0 },
  );
  assert.deepEqual(
    days.map((d) => d.day),
    ['2026-10-23', '2026-10-24'],
  );
  assert.equal(siteDataDelta(days, 'uploaded'), 8);
  assert.equal(siteDataDelta(days, 'downloaded'), -8);
  assert.equal(siteDataDelta(days, 'bonus'), null);
  assert.equal(
    siteDataDelta(
      siteDataDays([], { captured_at: '2026-10-24T00:00:00Z', uploaded: 0 }),
      'uploaded',
    ),
    0,
  );
  assert.equal(
    siteDataDelta(
      siteDataDays([{ stats: { captured_at: '2026-10-23', ratio_infinite: true } }], {
        captured_at: '2026-10-24',
        ratio: 1,
      }),
      'ratio',
    ),
    null,
  );
  assert.equal(siteDataBytes(-1024), '-1.00 KB');
  assert.equal(siteDataBytes(0, true), '+0.00 B');
});
