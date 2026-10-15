import test from 'node:test';
import assert from 'node:assert/strict';
import { deleteHistoryBatch } from '../src/features/history-delete.ts';

const row = (id, fileID = id) => ({
  id,
  file_id: fileID,
  media: { title: '测试剧', media_type: 'tv' },
  season: 1,
  episodes: [1],
  source: '/source/' + id + '.mkv',
  target: '/library/' + id + '.mkv',
  mode: 'hardlink',
  size: 10,
  status: 'success',
  updated_at: '',
});

test('batch deletes only selected executions with the chosen media-library scope', async () => {
  const requests = [];
  const progress = [];
  let inFlight = 0;
  const items = [row('stable-one', 'latest-one'), row('stable-two', 'latest-two')];
  const outcome = await deleteHistoryBatch(
    items,
    false,
    true,
    async (item, request) => {
      assert.equal(inFlight++, 0, 'destructive operations are sequential');
      await Promise.resolve();
      requests.push({ id: item.id, request });
      inFlight--;
      return { deleted: true };
    },
    (item, done, total) => progress.push({ id: item.id, done, total }),
  );
  assert.deepEqual(requests, [
    {
      id: 'stable-one',
      request: {
        confirm: true,
        file_id: 'latest-one',
        delete_source: false,
        delete_target: true,
      },
    },
    {
      id: 'stable-two',
      request: {
        confirm: true,
        file_id: 'latest-two',
        delete_source: false,
        delete_target: true,
      },
    },
  ]);
  assert.deepEqual(outcome.succeeded, items);
  assert.deepEqual(outcome.failed, []);
  assert.deepEqual(progress.at(-1), { id: 'stable-two', done: 2, total: 2 });
});

test('delete-all keeps failures available and continues remaining selected files without reporting all successful', async () => {
  const items = [row('one'), row('two'), row('three')];
  const requests = [];
  const changed = new Error('整理记录已更新，请刷新后重新确认删除');
  const outcome = await deleteHistoryBatch(
    items,
    true,
    true,
    async (item, request) => {
      requests.push({ id: item.id, request });
      if (item.id === 'two') throw changed;
      return { deleted: true };
    },
    () => {},
  );
  assert.deepEqual(
    requests.map(({ id }) => id),
    ['one', 'two', 'three'],
  );
  assert.ok(requests.every(({ request }) => request.delete_source && request.delete_target));
  assert.deepEqual(
    outcome.succeeded.map(({ id }) => id),
    ['one', 'three'],
  );
  assert.deepEqual(outcome.failed, [{ item: items[1], error: changed }]);
});

test('HTTP success without confirmed deletion remains a failed selection', async () => {
  const item = row('uncertain');
  const outcome = await deleteHistoryBatch(
    [item],
    false,
    false,
    async () => ({}),
    () => {},
  );
  assert.equal(outcome.succeeded.length, 0);
  assert.equal(outcome.failed[0].item, item);
  assert.match(outcome.failed[0].error.message, /未确认删除完成/);
});
