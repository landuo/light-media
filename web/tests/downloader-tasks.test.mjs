import test from 'node:test';
import assert from 'node:assert/strict';
import {
  downloadTaskInfo,
  downloadTimeLeft,
  downloadingTaskIsRunning,
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
