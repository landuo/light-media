import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {
  downloadTaskInfo,
  downloadTimeLeft,
  downloadingTaskIsRunning,
  downloadSourceFileSize,
} from '../src/features/downloader-tasks.ts';

test('download cards join history by downloader and hash, preserving the original resource title', () => {
  const task = { hash: 'ab12', name: 'Example.S02E03-E04.1080p.WEB-DL' };
  const data = {
    intents: [
      { downloader_id: 'other', hash: 'ab12', media: { title: 'Wrong downloader' } },
      {
        downloader_id: 'qb',
        hash: 'AB12',
        media: { title: 'Example', poster_path: '/poster.jpg' },
        season: 2,
        episodes: [3, 4],
      },
    ],
  };
  const info = downloadTaskInfo(data, 'qb', task);
  assert.equal(info.title, 'Example');
  assert.equal(info.subtitle, task.name);
  assert.equal(info.seasonEpisode, 'S02E03-E04');
  assert.equal(info.media.poster_path, '/poster.jpg');
  assert.equal(downloadTaskInfo({}, 'qb', task).title, task.name);
  assert.equal(downloadingTaskIsRunning({ status: 'queued' }), true);
  assert.equal(downloadingTaskIsRunning({ status: 'paused' }), false);
});

test('remaining time uses actual outstanding bytes and hides unknown or finished estimates', () => {
  assert.equal(
    downloadTimeLeft({ total_size: 4000, completed: 1000, download_speed: 10 }),
    '5分0秒',
  );
  assert.equal(downloadTimeLeft({ total_size: 4000, completed: 1000, download_speed: 0 }), '');
  assert.equal(downloadTimeLeft({ total_size: 4000, completed: 4000, download_speed: 10 }), '');
});

test('ordinary module DownloaderTorrent preserves its formatted fields and media metadata', () => {
  const task = {
    hash: 'owned',
    name: 'Release.S02E03.1080p',
    source_torrent: {
      title: 'Release.S02E03.1080p',
      name: 'Release',
      season_episode: 'S02E03',
      dlspeed: '2.00 MB',
      upspeed: '0 B',
      left_time: '1时0秒',
      media: { title: 'Source media', poster: '/source.jpg', backdrop: '/background.jpg' },
    },
  };
  const actual = downloadTaskInfo({}, 'owned', task);
  assert.equal(actual.title, 'Source media');
  assert.equal(actual.subtitle, task.source_torrent.title);
  assert.equal(actual.seasonEpisode, 'S02E03');
  assert.equal(actual.media.poster_path, '/source.jpg');
  assert.equal(downloadTimeLeft(task), '1时0秒');
  assert.equal(downloadTimeLeft({ ...task, source_torrent: { left_time: '' } }), '');
  assert.equal(
    downloadTaskInfo({}, 'owned', {
      name: 'raw fallback',
      source_torrent: { name: null, title: null },
    }).title,
    null,
  );
  assert.equal(
    downloadTaskInfo({}, 'owned', {
      name: 'raw fallback',
      source_torrent: { name: null, title: null },
    }).subtitle,
    null,
  );
  assert.equal(
    downloadTaskInfo({}, 'owned', {
      source_torrent: { media: { season: 'S02', episode: 'E03-E04' } },
    }).seasonEpisode,
    'S02 E03-E04',
  );
  const oracle = JSON.parse(
    fs.readFileSync(
      new URL(
        '../../testdata/standard-module-host/downloader-card-source-oracle.json',
        import.meta.url,
      ),
    ),
  );
  for (const sample of oracle.cases)
    assert.equal(downloadSourceFileSize(sample.input), sample.result);
});

test('task index preserves first non-cancelled downloader/hash match and scans records once', async () => {
  const { createDownloadTaskIndex } = await import('../src/features/downloader-tasks.ts');
  const row = { downloader_id: 'qb', hash: 'AbC', media: { title: 'first' } };
  const data = {
    intents: [
      { ...row, state: 'cancelled' },
      row,
      { ...row, media: { title: 'second' } },
      { ...row, downloader_id: 'other', media: { title: 'other' } },
    ],
    native_downloads: [{ ...row, media: { title: 'native' } }],
  };
  const index = createDownloadTaskIndex(data);
  assert.equal(downloadTaskInfo(data, 'qb', { hash: 'ABC' }, index).title, 'first');
  assert.equal(downloadTaskInfo(data, 'other', { hash: 'abc' }, index).title, 'other');
  assert.equal(
    downloadTaskInfo(data, 'missing', { hash: 'abc', name: 'fallback' }, index).title,
    'fallback',
  );
  let reads = 0;
  const records = Array.from({ length: 400 }, (_, i) => ({
    downloader_id: 'qb',
    get hash() {
      reads++;
      return String(i);
    },
  }));
  const snapshot = { intents: records.slice(0, 200), native_downloads: records.slice(200) },
    built = createDownloadTaskIndex(snapshot);
  for (let i = 0; i < 5000; i++) downloadTaskInfo(snapshot, 'qb', { hash: String(i) }, built);
  assert.equal(reads, 400);
});
test('actual DownloaderTasks SFC derives once per snapshot and reuses index', async () => {
  const { parse, compileScript } = await import('@vue/compiler-sfc'),
    ts = (await import('typescript')).default,
    vue = await import('vue'),
    helpers = await import('../src/features/downloader-tasks.ts'),
    flights = await import('../src/features/single-flight-request.ts');
  const source = fs.readFileSync(
      new URL('../src/components/DownloaderTasks.vue', import.meta.url),
      'utf8',
    ),
    { descriptor } = parse(source),
    module = { exports: {} };
  const compiled = ts.transpileModule(compileScript(descriptor, { id: 'tasks' }).content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  new Function('require', 'exports', compiled)((name) => {
    if (name === 'vue') return { ...vue, onMounted() {}, onUnmounted() {} };
    if (name === '../api') return { ui: { settings: { downloaders: [{ id: 'qb' }] } } };
    if (name === '../features/downloader-tasks') return helpers;
    if (name === '../features/single-flight-request') return flights;
    return {};
  }, module.exports);
  const app = module.exports.default.setup({}, { expose() {} });
  let reads = 0;
  app.data.value = {
    intents: Array.from({ length: 400 }, (_, i) => ({
      downloader_id: 'qb',
      get hash() {
        reads++;
        return String(i);
      },
      media: { title: String(i) },
    })),
    instances: [
      {
        id: 'qb',
        tasks: Array.from({ length: 1000 }, (_, i) => ({ hash: String(i), name: 'fallback' })),
      },
    ],
  };
  const rows = app.taskRows.value;
  assert.equal(rows.length, 1000);
  assert.equal(rows[399].info.title, '399');
  assert.equal(app.taskRows.value, rows);
  assert.equal(reads, 400);
  assert.match(source, /v-for="\{ task, info \} in taskRows"/);
  assert.doesNotMatch(source, /identity\(task\)/);
});
