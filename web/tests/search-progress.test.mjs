import test from 'node:test';
import assert from 'node:assert/strict';
import {
  interruptedSearch,
  siteSearchLabel,
  searchPending,
  siteElapsed,
} from '../src/features/search-progress.ts';
test('cancelled search preserves completed fast-site results and marks only unfinished sites cancelled', () => {
  const completed = { id: 'fast', status: 'ok', count: 1, elapsed_ms: 123 };
  const candidate = { resource: { id: 'one', site_id: 'fast' } };
  const result = interruptedSearch(
    {
      workflow_id: 'search',
      candidates: [candidate],
      sites: [completed, { id: 'slow', status: 'searching', count: 0 }],
    },
    ['fast', 'slow', 'queued'],
  );
  assert.equal(result.workflow_id, 'search');
  assert.deepEqual(result.candidates, [candidate]);
  assert.equal(result.sites[0], completed);
  assert.deepEqual(
    result.sites.map((s) => s.status),
    ['ok', 'cancelled', 'cancelled'],
  );
});
test('connection interruption never presents unfinished sites as successful or empty', () => {
  const result = interruptedSearch(null, ['first', 'second'], 'interrupted');
  assert.deepEqual(result.candidates, []);
  assert.deepEqual(
    result.sites.map((s) => s.status),
    ['interrupted', 'interrupted'],
  );
  assert.equal(siteSearchLabel('interrupted'), '连接中断');
  assert.equal(siteSearchLabel('timeout'), '超时');
  assert.equal(searchPending('searching'), true);
  assert.equal(searchPending('cancelled'), false);
  assert.equal(siteElapsed({ elapsed_ms: 2300 }), ' · 2.3 秒');
});
