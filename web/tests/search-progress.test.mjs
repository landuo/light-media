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

test('local deadline marks unfinished sites based on candidates already visible', () => {
  const done = { id: 'fast', status: 'ok', count: 1 };
  const err = { id: 'failed', status: 'failed', message: 'site error' };
  const candidates = [{ resource: { site_id: 'page', id: '1' } }];
  const snapshot = interruptedSearch(
    {
      candidates,
      sites: [
        done,
        err,
        { id: 'page', status: 'searching', count: 1 },
        { id: 'unfiltered', status: 'searching', count: 10 },
      ],
    },
    ['fast', 'failed', 'page', 'unfiltered', 'slow'],
    'timeout',
  );
  assert.strictEqual(snapshot.candidates, candidates);
  assert.strictEqual(snapshot.sites[0], done);
  assert.strictEqual(snapshot.sites[1], err);
  assert.deepEqual(
    snapshot.sites.map((s) => s.status),
    ['ok', 'failed', 'partial', 'timeout', 'timeout'],
  );
});
