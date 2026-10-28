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
