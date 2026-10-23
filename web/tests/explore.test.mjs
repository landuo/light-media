import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createExploreFilters,
  exploreQuery,
  exploreYears,
  normalizeExploreFilters,
  normalizeExploreOrder,
  anilistYears,
} from '../src/features/explore-filters.ts';
import {
  createExploreFeed,
  loadExplorePage,
  resetExploreFeed,
} from '../src/features/explore-feed.ts';

const media = (id, source = 'themoviedb', media_type = 'movie') => ({
  id,
  source_id: String(id),
  source,
  media_type,
});
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

test('TMDB sends original filters and drops only incompatible selections when media type changes', () => {
  const filters = createExploreFilters('themoviedb');
  assert.deepEqual(
    Object.fromEntries(new URLSearchParams(exploreQuery('themoviedb', filters, 1))),
    {
      source: 'themoviedb',
      type: 'movies',
      page: '1',
      sort_by: 'popularity.desc',
      with_genres: '',
      with_original_language: '',
      with_keywords: '',
      with_watch_providers: '',
      vote_average: '0',
      vote_count: '10',
      release_date: '',
    },
  );
  Object.assign(filters, {
    type: 'tvs',
    sort_by: 'release_date.desc',
    with_genres: '28',
    with_original_language: 'zh',
  });
  normalizeExploreFilters('themoviedb', filters);
  assert.equal(filters.sort_by, 'popularity.desc');
  assert.equal(filters.with_genres, '');
  assert.equal(filters.with_original_language, 'zh');
  Object.assign(filters, { type: 'movies', sort_by: 'vote_average.desc', with_genres: '16' });
  normalizeExploreFilters('themoviedb', filters);
  assert.equal(filters.sort_by, 'vote_average.desc');
  assert.equal(filters.with_genres, '16');
});

test('Douban combines source labels as tags; Bangumi preserves category zero and deselected sort', () => {
  const douban = createExploreFilters('douban');
  Object.assign(douban, { category: '喜剧', zone: '中国大陆', year: '2025', sort: '' });
  normalizeExploreFilters('douban', douban);
  assert.deepEqual(Object.fromEntries(new URLSearchParams(exploreQuery('douban', douban, 2))), {
    source: 'douban',
    type: 'movies',
    page: '2',
    sort: 'U',
    tags: '喜剧,中国大陆,2025',
  });
  const bangumi = createExploreFilters('bangumi');
  Object.assign(bangumi, { cat: '0', year: '2024', sort: '' });
  normalizeExploreFilters('bangumi', bangumi);
  assert.deepEqual(Object.fromEntries(new URLSearchParams(exploreQuery('bangumi', bangumi, 1))), {
    source: 'bangumi',
    type: '2',
    page: '1',
    sort: '',
    cat: '0',
    year: '2024',
  });
  assert.deepEqual(Object.keys(exploreYears('bangumi', 2026)), [
    '2017',
    '2018',
    '2019',
    '2020',
    '2021',
    '2022',
    '2023',
    '2024',
    '2025',
    '2026',
  ]);
});

test('tab order ignores retired sources and restores each known source exactly once', () => {
  assert.deepEqual(normalizeExploreOrder(['douban', 'obsolete', 'douban']), [
    'douban',
    'themoviedb',
    'bangumi',
    'anilist',
  ]);
});

test('AniList preserves original enumeration values, defaults and descending fifteen-year options', () => {
  const filters = createExploreFilters('anilist');
  Object.assign(filters, {
    sort: '',
    genre: 'Slice of Life',
    format: 'TV_SHORT',
    season: 'FALL',
    season_year: '2026',
    status: 'RELEASING',
    country: 'JP',
  });
  normalizeExploreFilters('anilist', filters);
  assert.deepEqual(Object.fromEntries(new URLSearchParams(exploreQuery('anilist', filters, 3))), {
    source: 'anilist',
    type: 'ANIME',
    page: '3',
    sort: 'POPULARITY_DESC',
    count: '20',
    genre: 'Slice of Life',
    format: 'TV_SHORT',
    season: 'FALL',
    season_year: '2026',
    status: 'RELEASING',
    country: 'JP',
  });
  const years = anilistYears(2026);
  assert.equal(years.length, 15);
  assert.deepEqual(years[0], ['2026', '2026']);
  assert.deepEqual(years.at(-1), ['2012', '2012']);
});

test('append deduplicates within and across pages while retaining different source identities', async () => {
  const feed = createExploreFeed(),
    pages = [];
  assert.equal(
    await loadExplorePage(feed, async (page) => {
      pages.push(page);
      return { results: [media(1), media(1)], has_more: true };
    }),
    'ok',
  );
  assert.equal(
    await loadExplorePage(feed, async (page) => {
      pages.push(page);
      return {
        results: [media(1), media(1, 'douban'), media(1, 'themoviedb', 'tv')],
        has_more: false,
      };
    }),
    'empty',
  );
  assert.deepEqual(pages, [1, 2]);
  assert.equal(feed.results.length, 3);
  assert.equal(feed.hasMore, false);
  await loadExplorePage(feed, () => assert.fail('finished feeds must not request more pages'));
});

test('duplicate-only pages continue until the upstream feed ends', async () => {
  const feed = createExploreFeed();
  await loadExplorePage(feed, async () => ({ results: [media(1)], has_more: true }));
  assert.equal(
    await loadExplorePage(feed, async () => ({ results: [media(1)], has_more: true })),
    'ok',
  );
  assert.equal(feed.page, 2);
  assert.equal(feed.results.length, 1);
  assert.equal(await loadExplorePage(feed, async () => ({ results: [] })), 'empty');
});

test('an explicitly continuing page can be empty after upstream entries are filtered', async () => {
  const feed = createExploreFeed(),
    requested = [];
  assert.equal(
    await loadExplorePage(feed, async (page) => {
      requested.push(page);
      return { results: [], has_more: true };
    }),
    'ok',
  );
  assert.equal(feed.hasMore, true);
  assert.equal(
    await loadExplorePage(feed, async (page) => {
      requested.push(page);
      return { results: [media(7, 'douban')], has_more: false };
    }),
    'empty',
  );
  assert.deepEqual(requested, [1, 2]);
  assert.deepEqual(
    feed.results.map((item) => item.id),
    [7],
  );
});

test('filter reset isolates late responses and leaves the new request loading state intact', async () => {
  const feed = createExploreFeed(),
    old = deferred(),
    current = deferred();
  const oldLoad = loadExplorePage(feed, () => old.promise);
  resetExploreFeed(feed);
  const currentLoad = loadExplorePage(feed, () => current.promise);
  old.resolve({ results: [media(99)], has_more: false });
  assert.equal(await oldLoad, 'empty');
  assert.equal(feed.loading, true);
  assert.deepEqual(feed.results, []);
  current.resolve({ results: [media(2)], has_more: true });
  assert.equal(await currentLoad, 'ok');
  assert.deepEqual(
    feed.results.map((item) => item.id),
    [2],
  );
  assert.equal(feed.page, 1);
  assert.equal(feed.loading, false);
});

test('failures preserve successful pages and retry the failed page with a released loading flag', async () => {
  const feed = createExploreFeed(),
    pages = [];
  await loadExplorePage(feed, async () => ({ results: [media(1)], has_more: true }));
  assert.equal(
    await loadExplorePage(feed, async (page) => {
      pages.push(page);
      throw Error('暂时不可用');
    }),
    'error',
  );
  assert.equal(feed.loading, false);
  assert.equal(feed.page, 1);
  assert.equal(feed.error, '暂时不可用');
  await loadExplorePage(feed, async (page) => {
    pages.push(page);
    return { results: [media(2)], has_more: false };
  });
  assert.deepEqual(pages, [2, 2]);
  assert.equal(feed.error, '');
  assert.deepEqual(
    feed.results.map((item) => item.id),
    [1, 2],
  );
});

test('source states remain independent and concurrent duplicate loads do not send extra requests', async () => {
  const tmdb = createExploreFeed(),
    douban = createExploreFeed(),
    pending = deferred();
  const loading = loadExplorePage(tmdb, () => pending.promise);
  await loadExplorePage(tmdb, () => assert.fail('duplicate load'));
  await loadExplorePage(douban, async () => ({ results: [media(3, 'douban')], has_more: false }));
  pending.resolve({ results: [media(4)], has_more: true });
  await loading;
  resetExploreFeed(tmdb);
  assert.equal(tmdb.page, 0);
  assert.deepEqual(
    douban.results.map((item) => item.id),
    [3],
  );
  assert.equal(douban.page, 1);
});
