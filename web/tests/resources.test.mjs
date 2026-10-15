import test from 'node:test';
import assert from 'node:assert/strict';
import {
  candidateKey,
  emptyResourceFilters,
  filterResources,
  groupResources,
  promotionKind,
  promotionRemaining,
} from '../src/features/resources.ts';
const item = (id, extra = {}) => ({
  resource: {
    id,
    site_id: 'site-a',
    title: 'Synthetic S01E03',
    size: 1024,
    seeders: 2,
    download_factor: 1,
    ...extra,
  },
  season: 1,
  episodes: [3],
  accepted: true,
});
test('resource filters include all candidates by default and keep unknown distinct from zero', () => {
  const rows = [
    item('unknown', { seeders: -1, download_factor: undefined }),
    item('zero', { seeders: 0 }),
    item('alive', { seeders: 9 }),
  ];
  assert.equal(filterResources(rows, emptyResourceFilters(), 'default', true).length, 3);
  assert.deepEqual(
    filterResources(rows, { ...emptyResourceFilters(), seeders: 'unknown' }, 'default', true).map(
      (x) => x.resource.id,
    ),
    ['unknown'],
  );
  assert.deepEqual(
    filterResources(rows, { ...emptyResourceFilters(), seeders: 'zero' }, 'default', true).map(
      (x) => x.resource.id,
    ),
    ['zero'],
  );
  assert.deepEqual(
    filterResources(rows, emptyResourceFilters(), 'seeders', false).map((x) => x.resource.id),
    ['zero', 'alive', 'unknown'],
  );
  assert.equal(promotionKind(rows[0].resource), 'unknown');
});
test('cross-site groups require exact release identity and downloads use actual site-resource references', () => {
  const a = item('1'),
    b = item('2', { site_id: 'site-b' }),
    other = item('3', { size: 2048 }),
    noSize = item('4', { size: 0 });
  assert.deepEqual(
    groupResources([a, b, other, noSize]).map((x) => x.length),
    [2, 1, 1],
  );
  const state = { [candidateKey(a)]: 'downloading' };
  assert.deepEqual(
    filterResources(
      [a, b],
      { ...emptyResourceFilters(), state: 'downloaded' },
      'default',
      true,
      state,
    ),
    [a],
  );
  assert.deepEqual(
    filterResources([a, b], { ...emptyResourceFilters(), state: 'new' }, 'default', true, state),
    [b],
  );
});
test('promotion remaining is derived only from supplied expiry', () => {
  assert.equal(promotionRemaining('invalid', 0), '');
  assert.equal(
    promotionRemaining('2026-10-15T02:00:00Z', Date.parse('2026-10-15T00:00:00Z')),
    '剩余 2小时',
  );
  assert.equal(
    promotionRemaining('2026-10-15T00:00:00Z', Date.parse('2026-10-15T01:00:00Z')),
    '已到期',
  );
});

test('single-resource download takes the explicit range or only a same-season parsed hint, never an implicit whole season', async () => {
  const { suggestedDownloadEpisodes } = await import('../src/features/resources.ts');
  const c = { resource: { site_id: 'a', id: 'one' }, season: 2, episodes: [17] };
  assert.deepEqual(suggestedDownloadEpisodes([], c, 2), [17]);
  assert.deepEqual(suggestedDownloadEpisodes([4, 5], c, 2), [4, 5]);
  assert.deepEqual(suggestedDownloadEpisodes([], c, 1), []);
  assert.deepEqual(suggestedDownloadEpisodes([], { ...c, episodes: [] }, 2), []);
  assert.deepEqual(suggestedDownloadEpisodes([], { ...c, season: 0, episodes: [260] }, 2), []);
  assert.deepEqual(suggestedDownloadEpisodes([], { ...c, episodes: [0] }, 2), []);
});
