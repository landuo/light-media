import test from 'node:test';
import assert from 'node:assert/strict';
import { subscriptionTarget, subscriptionTotal } from '../src/features/subscriptions.ts';
test('new TV subscriptions track a season from an inclusive starting episode without a fixed episode list', () => {
  const tv = { media: { media_type: 'tv' }, start_episode: 17, episodes: [1, 2] };
  assert.deepEqual(subscriptionTarget(tv), {
    target_scope: 'season',
    start_episode: 17,
    episodes: [],
  });
  assert.deepEqual(subscriptionTarget({ media: { media_type: 'tv' } }), {
    target_scope: 'season',
    start_episode: 1,
    episodes: [],
  });
  assert.throws(() => subscriptionTarget({ ...tv, start_episode: 0 }));
  assert.throws(() => subscriptionTarget({ ...tv, start_episode: 2.5 }));
});
test('legacy explicit episode ranges remain unchanged until the user converts them', () => {
  const legacy = {
    id: 'synthetic',
    media: { media_type: 'tv' },
    episodes: [3, 5],
    start_episode: 1,
  };
  assert.deepEqual(subscriptionTarget(legacy), {
    target_scope: 'episodes',
    start_episode: 1,
    episodes: [3, 5],
  });
  assert.deepEqual(subscriptionTarget(legacy, true), {
    target_scope: 'season',
    start_episode: 1,
    episodes: [],
  });
  assert.deepEqual(subscriptionTarget({ ...legacy, media: { media_type: 'movie' } }), {
    target_scope: 'movie',
    start_episode: 0,
    episodes: [],
  });
});
test('subscription progress counts the actual target episodes and excludes explicit exclusions', () => {
  assert.equal(
    subscriptionTotal({
      media: { media_type: 'tv' },
      known_episodes: [17, 18, 19],
      excluded_episodes: [18],
    }),
    2,
  );
  assert.equal(
    subscriptionTotal({
      media: { media_type: 'tv', seasons: [{ season_number: 2, episode_count: 20 }] },
      season: 2,
      start_episode: 17,
    }),
    4,
  );
  assert.equal(subscriptionTotal({ media: { media_type: 'movie' } }), 1);
});

test('multi-season creation retains individual success, conflict and failure without retrying a successful season', async () => {
  const { submitSeasonBatch } = await import('../src/features/subscriptions.ts');
  const rows = [1, 2, 3].map((season) => ({
    season,
    title: 'Season ' + season,
    selected: true,
    start_episode: season === 2 ? 5 : 1,
    state: 'ready',
    message: '',
  }));
  const calls = [];
  await submitSeasonBatch(
    rows,
    { media: { media_type: 'tv' }, episodes: [], target_scope: 'season' },
    async (req) => {
      calls.push(req);
      if (req.season === 2)
        throw Object.assign(new Error('已存在'), { code: 'SUBSCRIPTION_EXISTS' });
      if (req.season === 3) throw new Error('连接失败');
      return { id: 'created-one' };
    },
  );
  assert.deepEqual(
    rows.map((r) => r.state),
    ['success', 'conflict', 'failed'],
  );
  assert.equal(rows[0].id, 'created-one');
  assert.equal(calls[1].start_episode, 5);
  assert.equal(calls[0].target_scope, 'season');
  assert.deepEqual(calls[0].episodes, []);
  const retries = [];
  await submitSeasonBatch(rows, { media: { media_type: 'tv' } }, async (req) => {
    retries.push(req.season);
    return { id: 'created-three' };
  });
  assert.deepEqual(retries, [3]);
  assert.deepEqual(
    rows.map((r) => r.state),
    ['success', 'conflict', 'success'],
  );
});
