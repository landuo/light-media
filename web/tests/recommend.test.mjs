import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createExploreFeed,
  loadExplorePage,
  resetExploreFeed,
} from '../src/features/explore-feed.ts';
import {
  ensureRecommendShelf,
  recommendEnabled,
  recommendSourceVisible,
  recommendPath,
  recommendBrowseRoute,
  recommendBrowseSource,
  saveRecommendPreferences,
} from '../src/features/recommend.ts';

const sources = [
  { id: 'tmdb_movies', title: 'TMDB热门电影', category: 'movie' },
  { id: 'douban_tv_hot', title: '豆瓣热门电视剧', category: 'tv' },
  { id: 'anilist_trending', title: 'AniList 当前趋势', category: 'anime' },
];
const media = (id, source = 'themoviedb') => ({
  id,
  source_id: String(id),
  source,
  media_type: 'tv',
});
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

test('preference migration keeps explicit opt-outs and enables newly available shelves', () => {
  const enabled = recommendEnabled(sources, { tmdb_movies: false, retired: false });
  assert.deepEqual(enabled, { tmdb_movies: false, douban_tv_hot: true, anilist_trending: true });
  assert.deepEqual(
    sources
      .filter((source) => recommendSourceVisible(source, 'all', enabled))
      .map((source) => source.id),
    ['douban_tv_hot', 'anilist_trending'],
  );
  assert.deepEqual(
    sources
      .filter((source) => recommendSourceVisible(source, 'anime', enabled))
      .map((source) => source.id),
    ['anilist_trending'],
  );
  assert.equal(recommendSourceVisible(sources[0], 'movie', enabled), false);
});

test('hidden and repeated intersection callbacks share one first-page request and retain native identities', async () => {
  const feed = createExploreFeed(),
    pending = deferred();
  let count = 0;
  const request = (page) => {
    count++;
    assert.equal(page, 1);
    return pending.promise;
  };
  await ensureRecommendShelf(feed, false, request);
  assert.equal(count, 0);
  const first = ensureRecommendShelf(feed, true, request);
  await ensureRecommendShelf(feed, true, request);
  await ensureRecommendShelf(feed, false, request);
  assert.equal(count, 1);
  pending.resolve({ results: [media(4), media(4), media(4, 'douban')], has_more: true });
  await first;
  await ensureRecommendShelf(feed, true, request);
  assert.equal(count, 1);
  assert.deepEqual(
    feed.results.map((item) => [item.source, item.source_id]),
    [
      ['themoviedb', '4'],
      ['douban', '4'],
    ],
  );
  assert.equal(feed.page, 1);
});

test('returning to empty or failed shelves does not refetch, and explicit retry remains possible', async () => {
  const empty = createExploreFeed(),
    failed = createExploreFeed();
  await ensureRecommendShelf(empty, true, async () => ({ results: [], has_more: false }));
  await ensureRecommendShelf(empty, true, () => assert.fail('empty shelves must stay loaded'));
  await ensureRecommendShelf(failed, true, async () => {
    throw Error('请求失败');
  });
  await ensureRecommendShelf(failed, true, () =>
    assert.fail('failed shelves require an explicit retry'),
  );
  assert.equal(failed.error, '请求失败');
  await loadExplorePage(failed, async (page) => {
    assert.equal(page, 1);
    return { results: [media(5, 'bangumi')], has_more: false };
  });
  assert.equal(failed.error, '');
  assert.equal(failed.loaded, true);
});

test('disposed shelf response cannot repopulate a reset feed or clear a new request', async () => {
  const feed = createExploreFeed(),
    old = deferred(),
    current = deferred();
  const first = ensureRecommendShelf(feed, true, () => old.promise);
  resetExploreFeed(feed);
  const second = ensureRecommendShelf(feed, true, () => current.promise);
  old.resolve({ results: [media(1)], has_more: false });
  await first;
  assert.deepEqual(feed.results, []);
  assert.equal(feed.loading, true);
  current.resolve({ results: [media(2, 'anilist')], has_more: false });
  await second;
  assert.equal(feed.results[0].source, 'anilist');
  assert.equal(feed.results[0].id, 2);
});

test('saving preferences commits a snapshot only after success and rejects simultaneous submits', async () => {
  const state = { enabled: recommendEnabled(sources), saving: false, error: '' };
  const draft = { ...state.enabled, tmdb_movies: false };
  const pending = deferred();
  let submitted;
  const first = saveRecommendPreferences(state, sources, draft, (value) => {
    submitted = value;
    return pending.promise;
  });
  assert.equal(state.enabled.tmdb_movies, true);
  draft.douban_tv_hot = false;
  assert.equal(
    await saveRecommendPreferences(state, sources, draft, () => assert.fail('duplicate save')),
    false,
  );
  pending.resolve({});
  assert.equal(await first, true);
  assert.deepEqual(state.enabled, submitted);
  assert.equal(state.enabled.douban_tv_hot, true);
  assert.equal(state.enabled.tmdb_movies, false);
  assert.equal(state.saving, false);
});

test('failed preference save preserves visible shelves and leaves a retryable draft', async () => {
  const state = { enabled: recommendEnabled(sources), saving: false, error: '' };
  const draft = Object.fromEntries(sources.map((source) => [source.id, false]));
  assert.equal(
    await saveRecommendPreferences(state, sources, draft, async () => {
      throw Error('暂时不可用');
    }),
    false,
  );
  assert.equal(state.error, '暂时不可用');
  assert.equal(state.saving, false);
  assert.ok(Object.values(state.enabled).every(Boolean));
  assert.ok(Object.values(draft).every((enabled) => !enabled));
  assert.equal(await saveRecommendPreferences(state, sources, draft, async () => ({})), true);
  assert.equal(state.error, '');
  assert.ok(Object.values(state.enabled).every((enabled) => !enabled));
});

test('more routes retain source identifiers and titles separately from API paging', () => {
  const source = { id: 'anilist_trending', title: 'AniList 当前趋势', category: 'anime' };
  const route = recommendBrowseRoute(source);
  assert.deepEqual(recommendBrowseSource(route), { id: source.id, title: source.title });
  assert.deepEqual(
    Object.fromEntries(new URLSearchParams(recommendPath(source.id, 3).split('?')[1])),
    { id: 'anilist_trending', page: '3' },
  );
  assert.equal(recommendBrowseSource('recommend-browse/%broken').id, '');
});
