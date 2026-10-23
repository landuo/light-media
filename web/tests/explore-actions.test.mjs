import test from 'node:test';
import assert from 'node:assert/strict';
import {
  cancelExploreSubscriptions,
  enabledExploreSites,
  exploreMovieIntent,
  exploreResolvePath,
  exploreResourceRoute,
  exploreSeasonRows,
  exploreSubscriptionBody,
  exploreSubscriptions,
  pendingExploreMovieMutation,
  rememberExploreSites,
  selectedExploreSites,
  submitExploreSeasons,
  tmdbExploreMedia,
} from '../src/features/explore-actions.ts';

test('provider IDs never enter TMDB search or subscription requests without resolution', () => {
  const source = { source: 'anilist', source_id: '151', id: 0, media_type: 'tv' };
  assert.equal(tmdbExploreMedia(source), null);
  assert.equal(tmdbExploreMedia({ ...source, id: 151 }), null);
  assert.throws(() => exploreSubscriptionBody(source));
  assert.throws(() => exploreResourceRoute(source, ['one'], [{ id: 'one', name: 'One' }]));
  assert.equal(
    exploreResolvePath(source),
    '/catalog/explore/resolve?source=anilist&kind=tv&source_id=151',
  );
  assert.throws(() => exploreResolvePath({ ...source, source_id: '151&source=themoviedb' }));
  const resolved = tmdbExploreMedia({ id: 42, media_type: 'tv', title: 'Resolved' });
  const body = exploreSubscriptionBody(resolved);
  assert.equal(body.tmdb_id, 42);
  assert.equal(body.media.id, 42);
  assert.equal(body.target_scope, 'season');
  assert.equal(body.start_episode, 1);
  assert.deepEqual(body.episodes, []);
  assert.equal(source.id, 0);
  assert.equal(source.source_id, '151');
});

test('search normalizes managed-site rows, excludes disabled sites and encodes selected IDs', () => {
  const sites = enabledExploreSites({
    sites: [
      { config: { id: 'one', name: 'One', enabled: true } },
      { config: { id: 'disabled', name: 'Disabled', enabled: false } },
      { config: { id: 'two', name: 'Two', enabled: true } },
      { config: { id: 'one', name: 'Duplicate', enabled: true } },
    ],
  });
  assert.deepEqual(sites, [
    { id: 'one', name: 'One' },
    { id: 'two', name: 'Two' },
  ]);
  assert.equal(
    exploreResourceRoute(
      { id: 42, media_type: 'movie' },
      ['two', 'one', 'two', 'disabled', 'gone'],
      sites,
    ),
    'resources/movie/42?sites=two%2Cone',
  );
  assert.throws(() => exploreResourceRoute({ id: 42, media_type: 'movie' }, ['disabled'], sites));
  assert.deepEqual(selectedExploreSites(sites, ['two', 'disabled']), ['two']);
  rememberExploreSites(['one'], sites);
  assert.deepEqual(selectedExploreSites(sites, ['two']), ['one']);
  assert.deepEqual(selectedExploreSites([{ id: 'two', name: 'Two' }], ['two']), []);
});

test('series cancellation retains failed seasons and never retries successful or replacement subscriptions', async () => {
  const original = [
    { id: 's1', season: 1 },
    { id: 's2', season: 2 },
  ];
  const attempts = [];
  const first = await cancelExploreSubscriptions(
    ['s1', 's2'],
    original,
    async (id) => {
      attempts.push(id);
      if (id === 's2') throw new Error('Temporary error');
      return { cancelled: true };
    },
    () => {},
    (error) => error.message,
  );
  assert.deepEqual(attempts, ['s1', 's2']);
  assert.deepEqual(first.remaining, [{ id: 's2', season: 2 }]);
  assert.equal(first.failures.length, 1);
  const retried = [];
  const second = await cancelExploreSubscriptions(
    ['s1', 's2'],
    [...first.remaining, { id: 'replacement', season: 1 }],
    async (id) => {
      retried.push(id);
      return { cancelled: true };
    },
    () => {},
    (error) => error.message,
  );
  assert.deepEqual(retried, ['s2']);
  assert.deepEqual(second.remaining, [{ id: 'replacement', season: 1 }]);
  assert.deepEqual(second.failures, []);
});

test('movie retries preserve create/cancel intent after the first request may have committed', () => {
  const create = exploreMovieIntent([]);
  const committed = [{ id: 'created-before-disconnect' }];
  assert.equal(exploreMovieIntent(committed, create), create);
  assert.equal(pendingExploreMovieMutation(create, committed), null);
  const cancel = exploreMovieIntent([{ id: 'old' }]);
  assert.equal(pendingExploreMovieMutation(cancel, [{ id: 'old' }]), 'cancel');
  assert.equal(exploreMovieIntent([], cancel), cancel);
  assert.equal(pendingExploreMovieMutation(cancel, []), null);
  assert.equal(pendingExploreMovieMutation(cancel, [{ id: 'replacement' }]), null);
  const body = exploreSubscriptionBody({ id: 42, media_type: 'movie', title: 'Movie', year: 2026 });
  assert.equal(body.target_scope, 'movie');
  assert.equal(body.season, 0);
  assert.equal(body.start_episode, 0);
  assert.equal('downloader_id' in body, false);
  assert.equal('policy' in body, false);
});

test('seasons exclude specials and existing subscriptions while failed batches retry only unfinished seasons', async () => {
  const media = {
    id: 42,
    media_type: 'tv',
    title: 'Series',
    seasons: [
      { season_number: 0, episode_count: 2 },
      { season_number: 1, episode_count: 12 },
      { season_number: 2, episode_count: 10 },
      { season_number: 3, episode_count: 8 },
      { season_number: 3, episode_count: 8 },
    ],
  };
  const subscriptions = exploreSubscriptions(media, {
    items: [
      { id: 'one', media, season: 1, state: 'paused' },
      { id: 'old-two', media, season: 2, state: 'cancelled' },
      { id: 'different', media: { id: 99, media_type: 'tv' }, season: 2, state: 'active' },
    ],
  });
  const rows = exploreSeasonRows(media, subscriptions);
  assert.deepEqual(
    rows.map((row) => [row.season, row.state]),
    [
      [1, 'conflict'],
      [2, 'ready'],
      [3, 'ready'],
    ],
  );
  rows.forEach((row) => {
    row.selected = true;
  });
  const calls = [],
    changed = [];
  const describe = (error) => error.message;
  await submitExploreSeasons(
    rows,
    media,
    async (body) => {
      calls.push(body);
      if (body.season === 3) throw new Error('Temporary error');
      return { id: 'two', season: body.season };
    },
    (value) => changed.push(value.id),
    describe,
  );
  assert.deepEqual(
    calls.map((body) => body.season),
    [2, 3],
  );
  assert.equal(calls[0].total_episodes, 10);
  assert.equal(calls[0].tmdb_id, 42);
  assert.equal(rows[1].state, 'success');
  assert.equal(rows[2].state, 'failed');
  const retried = [];
  await submitExploreSeasons(
    rows,
    media,
    async (body) => {
      retried.push(body.season);
      return { id: 'three' };
    },
    (value) => changed.push(value.id),
    describe,
  );
  assert.deepEqual(retried, [3]);
  assert.deepEqual(changed, ['two', 'three']);
});
