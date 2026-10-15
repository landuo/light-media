import test from 'node:test';
import assert from 'node:assert/strict';
import {
  fillBatchNumbering,
  importEntryRequest,
  previewOrganizeEntries,
  confirmOrganizeEntries,
} from '../src/features/file-organize.ts';

test('TV batch numbering uses parsed episodes for offsets and natural filename order for consecutive numbers', () => {
  const files = [
    {
      path: '/own/show10.mkv',
      season: 1,
      episodes: '10',
      parsed: { original: { episodes: [110] } },
    },
    { path: '/own/show2.mkv', season: 1, episodes: '2', parsed: { original: { episodes: [102] } } },
  ];
  const selected = files.map((file) => file.path);
  const offset = fillBatchNumbering(files, selected, {
    season: 2,
    mode: 'offset',
    offset: -100,
    start: 1,
  });
  assert.deepEqual(
    offset.map((file) => [file.season, file.episodes]),
    [
      [2, '10'],
      [2, '2'],
    ],
  );
  assert.deepEqual(
    fillBatchNumbering(offset, selected, { season: 2, mode: 'offset', offset: -100, start: 1 }).map(
      (file) => file.episodes,
    ),
    ['10', '2'],
  );
  const consecutive = fillBatchNumbering(files, selected, {
    season: 3,
    mode: 'sequential',
    offset: 0,
    start: 7,
  });
  assert.deepEqual(
    consecutive.map((file) => [file.season, file.episodes]),
    [
      [3, '8'],
      [3, '7'],
    ],
  );
  assert.deepEqual(
    files.map((file) => file.season),
    [1, 1],
  );
  assert.throws(
    () =>
      fillBatchNumbering(files, selected, { season: 1, mode: 'offset', offset: -200, start: 1 }),
    /超出/,
  );
  assert.throws(
    () =>
      fillBatchNumbering(
        [{ path: '/own/no-episode.mkv', season: 1, episodes: '' }],
        ['/own/no-episode.mkv'],
        { season: 1, mode: 'offset', offset: 1, start: 1 },
      ),
    /唯一/,
  );
});

test('batch organize requires an explicit identity and keeps previews separate from confirmed submissions', async () => {
  const entries = [
    {
      path: '/own/movie-one.mkv',
      season: 0,
      episodes: '',
      identity: { id: 101, media_type: 'movie' },
      rootID: 'movies',
    },
    {
      path: '/own/movie-two.mkv',
      season: 0,
      episodes: '',
      identity: { id: 102, media_type: 'movie' },
      rootID: 'movies',
    },
    { path: '/own/unconfirmed.mkv', season: 0, episodes: '', rootID: 'movies' },
  ];
  const calls = [];
  let failSecond = true;
  const invoke = async (path, body) => {
    calls.push({ path, body });
    if (path === '/import/preview')
      return {
        id: 'plan-' + body.tmdb_id,
        files: [{ id: 'file-' + body.tmdb_id, status: 'ready' }],
      };
    if (body.plan_id === 'plan-102' && failSecond) {
      failSecond = false;
      throw new Error('synthetic network failure');
    }
    return { id: 'job-' + body.plan_id };
  };
  await previewOrganizeEntries(entries, 'hardlink', invoke);
  assert.equal(calls.length, 2);
  assert.ok(calls.every((call) => call.path === '/import/preview'));
  assert.equal(entries[2].state, 'failed');
  assert.match(entries[2].error, /明确选择/);
  assert.deepEqual(calls[0].body.files, [{ path: '/own/movie-one.mkv', season: 0, episodes: [] }]);
  await confirmOrganizeEntries(entries, invoke);
  assert.equal(entries[0].state, 'queued');
  assert.equal(entries[1].state, 'submit_failed');
  await confirmOrganizeEntries(entries, invoke);
  assert.equal(entries[1].state, 'queued');
  assert.equal(
    calls.filter((call) => call.path === '/import/execute' && call.body.plan_id === 'plan-101')
      .length,
    1,
  );
  assert.equal(
    calls.filter((call) => call.path === '/import/execute' && call.body.plan_id === 'plan-102')
      .length,
    2,
  );
  await previewOrganizeEntries(entries, 'copy', invoke);
  assert.equal(calls.filter((call) => call.path === '/import/preview').length, 2);
});

test('TV single-file requests carry confirmed numbering mappings and never silently submit merged episodes', () => {
  const entry = {
    path: '/own/show.mkv',
    season: 2,
    episodes: '3',
    identity: { id: 103, media_type: 'tv' },
    rootID: 'shows',
    mappings: [{ id: 'own-mapping', confirmed: true }],
  };
  assert.deepEqual(importEntryRequest(entry, 'copy'), {
    media_type: 'tv',
    tmdb_id: 103,
    root_id: 'shows',
    mode: 'copy',
    files: [{ path: '/own/show.mkv', season: 2, episodes: [3] }],
    mappings: entry.mappings,
  });
  assert.throws(() => importEntryRequest({ ...entry, episodes: '3-4' }, 'copy'), /单集/);
  assert.deepEqual(importEntryRequest({ ...entry, episodes: '' }, 'copy').files[0].episodes, []);
});

test('individual preview blocks duplicate destinations before any confirmation request', async () => {
  const entries = [101, 102].map((id) => ({
    path: `/own/${id}.mkv`,
    season: 0,
    episodes: '',
    identity: { id, media_type: 'movie' },
    rootID: 'movies',
  }));
  let requests = 0;
  await previewOrganizeEntries(entries, 'hardlink', async (path, body) => {
    requests++;
    assert.equal(path, '/import/preview');
    return {
      id: `plan-${body.tmdb_id}`,
      files: [{ id: `file-${body.tmdb_id}`, status: 'ready', target: '/own/same-target.mkv' }],
    };
  });
  assert.equal(requests, 2);
  assert.ok(entries.every((entry) => entry.state === 'blocked' && entry.selectedIDs.length === 0));
  await confirmOrganizeEntries(entries, async () => {
    throw new Error('must never submit duplicate target');
  });
  assert.equal(requests, 2);
});

test('manual organize can leave identity and target automatic or submit a literal target path', async () => {
  const automatic = {
    path: '/downloads/Movie (2024).mkv',
    season: 0,
    episodes: '',
    automatic: true,
    targetPath: '',
  };
  assert.deepEqual(importEntryRequest(automatic, ''), {
    media_type: '',
    tmdb_id: 0,
    target_path: '',
    mode: '',
    files: [{ path: automatic.path, season: 0, episodes: [] }],
    mappings: [],
  });
  const chosen = {
    ...automatic,
    automatic: false,
    identity: { id: 101, media_type: 'movie' },
    targetPath: '/media/电影',
  };
  const request = importEntryRequest(chosen, 'copy');
  assert.equal(request.target_path, '/media/电影');
  assert.equal(Object.hasOwn(request, 'root_id'), false);
  assert.equal(importEntryRequest({ ...automatic, mediaType: 'tv' }, '').media_type, 'tv');
  let executions = 0;
  const entries = [{ ...automatic }];
  await previewOrganizeEntries(entries, '', async (path) => {
    if (path === '/import/execute') executions++;
    throw new Error('未找到唯一匹配的影片');
  });
  assert.equal(entries[0].state, 'failed');
  assert.match(entries[0].error, /唯一匹配/);
  await confirmOrganizeEntries(entries, async () => {
    executions++;
    return { id: 'unexpected' };
  });
  assert.equal(executions, 0);
});

test('manual organize preserves explicit false switches and leaves automatic switches unset', () => {
  const entry = {
    path: '/downloads/Test.mkv',
    season: 0,
    episodes: '',
    automatic: true,
    targetPath: '',
  };
  const automatic = importEntryRequest(
    { ...entry, renaming: null, scraping: null, libraryTypeFolder: null },
    '',
  );
  assert.equal(Object.hasOwn(automatic, 'renaming'), false);
  assert.equal(Object.hasOwn(automatic, 'scraping'), false);
  const explicit = importEntryRequest(
    { ...entry, renaming: false, scraping: false, libraryTypeFolder: true },
    'copy',
  );
  assert.equal(explicit.renaming, false);
  assert.equal(explicit.scraping, false);
  assert.equal(explicit.library_type_folder, true);
});
