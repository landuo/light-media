import test from 'node:test';
import assert from 'node:assert/strict';
import {
  historyViewState,
  historyTitle,
  historyMediaType,
  historyViewHash,
  historyQuery,
  sortHistory,
  historyGroups,
  historyPageButtons,
  transferQueueState,
  groupTransferQueue,
} from '../src/features/transfer-history.ts';

test('history search, pagination and grouped view survive a copied URL and browser navigation', () => {
  const state = { search: '电视剧*2026?', page: 5, pageSize: 500, grouped: true };
  assert.deepEqual(historyViewState(historyViewHash(state)), state);
  assert.deepEqual(historyViewState('#history?currentPage=-2&itemsPerPage=200'), {
    search: '',
    page: 1,
    pageSize: 50,
    grouped: false,
  });
  assert.deepEqual(historyPageButtons(50, 50), [44, 45, 46, 47, 48, 49, 50]);
  assert.deepEqual(historyPageButtons(1, 2), [1, 2]);
});

test('queue tabs merge media title/year across jobs and retain each file outcome while unknown files remain separate', () => {
  const input = [
    {
      id: 'job-one',
      title: '同一部剧',
      media: { title: '同一部剧', year: 2026 },
      status: 'running',
      error: 'a stale job error must not override a running file',
      files: [
        {
          id: 'file-one',
          source: '/source/E01.mkv',
          status: 'ready',
          job_id: 'job-one',
          job_state: 'running',
          can_cancel: true,
        },
      ],
    },
    {
      id: 'job-two',
      title: '同一部剧',
      media: { title: '同一部剧', year: 2026 },
      status: 'failed',
      error: 'synthetic disk failure',
      files: [{ id: 'file-two', source: '/source/E02.mkv', status: 'failed', can_cancel: false }],
    },
    {
      id: 'job-remake',
      title: '同一部剧',
      media: { title: '同一部剧', year: 2024 },
      status: 'succeeded',
      files: [{ id: 'remake', source: '/source/old.mkv', status: 'imported' }],
    },
    {
      id: 'unknown',
      title: '',
      media: {},
      status: 'failed',
      files: [
        { id: 'unknown-one', source: '/source/UnknownA.mkv', status: 'blocked' },
        { id: 'unknown-two', source: '/source/UnknownB.mkv', status: 'blocked' },
      ],
    },
  ];
  const groups = groupTransferQueue(input);
  assert.equal(groups.length, 4);
  assert.equal(groups[0].files.length, 2);
  assert.equal(groups[0].files[0].job_id, 'job-one');
  assert.equal(groups[0].files[0].job_state, 'running');
  assert.equal(groups[0].files[0].can_cancel, true);
  assert.equal(groups[0].files[0].error, '');
  assert.equal(groups[0].files[1].job_id, 'job-two');
  assert.equal(groups[0].files[1].job_state, 'failed');
  assert.equal(groups[0].files[1].error, 'synthetic disk failure');
  assert.notEqual(groups[0].id, groups[1].id);
  assert.deepEqual(
    groups.slice(2).map((group) => group.title),
    ['UnknownA.mkv', 'UnknownB.mkv'],
  );
  assert.equal(input[1].files[0].error, undefined);
});

test('unrecognized failures use their actual filename without inventing a movie identity', () => {
  const failed = { media: {}, source: '/source/Unrecognized.Release.mkv', target: '' };
  assert.equal(historyTitle(failed), 'Unrecognized.Release.mkv');
  assert.equal(historyMediaType(failed), '未识别');
  assert.equal(
    historyTitle({ ...failed, media: { title: '已确认片名', media_type: 'tv' } }),
    '已确认片名',
  );
  assert.equal(historyMediaType({ ...failed, media: { media_type: 'tv' } }), '电视剧');
});

test('queue follows actual job state while preserving completed or blocked per-file outcomes', () => {
  assert.equal(transferQueueState({ status: 'ready', job_state: 'running' }), 'running');
  assert.equal(transferQueueState({ status: 'ready', job_state: 'failed' }), 'failed');
  assert.equal(transferQueueState({ status: 'failed', job_state: 'retry_wait' }), 'waiting');
  assert.equal(transferQueueState({ status: 'imported', job_state: 'running' }), 'completed');
  assert.equal(transferQueueState({ status: 'blocked', job_state: 'running' }), 'failed');
  assert.equal(transferQueueState({ status: 'error', job_state: 'running' }), 'failed');
  assert.equal(transferQueueState({ status: 'ready', job_state: 'cancelled' }), 'cancelled');
});

test('original status search suggestions select status without searching literal Chinese status text', () => {
  const state = { search: '失败', page: 1, pageSize: 1000, grouped: false };
  const params = new URLSearchParams(historyQuery(state));
  assert.equal(params.get('status'), 'failed');
  assert.equal(params.get('q'), '');
  assert.equal(params.get('page_size'), '1000');
  assert.equal(params.get('capabilities'), 'deferred');
  state.search = '*.mkv';
  assert.equal(new URLSearchParams(historyQuery(state)).get('q'), '*.mkv');
});

test('history title sorting uses season and episode numbers and grouping retains every selected file', () => {
  const make = (id, season, episode, size) => ({
    id,
    media: { title: '测试剧', media_type: 'tv' },
    season,
    episodes: [episode],
    source: id,
    target: '',
    mode: 'copy',
    size,
    status: 'success',
    updated_at: '',
  });
  const rows = [make('s10', 10, 1, 5), make('e10', 2, 10, 10), make('e2', 2, 2, 20)];
  assert.deepEqual(
    sortHistory(rows, 'title', false).map((row) => row.id),
    ['e2', 'e10', 's10'],
  );
  assert.deepEqual(
    sortHistory(rows, 'size', true).map((row) => row.id),
    ['e2', 'e10', 's10'],
  );
  assert.equal(historyGroups(rows).length, 1);
  assert.equal(historyGroups(rows)[0].items.length, 3);
  assert.deepEqual(
    rows.map((row) => row.id),
    ['s10', 'e10', 'e2'],
  );
});

// The same query builder is used by initial loads, search, pagination and
// background polling, so no refresh may silently restore source tree scans.
test('every history page query defers execution capability checks', () => {
  for (const search of ['', '成功', '失败', '*.mkv']) {
    for (const pageSize of [25, 50, 100, 500, 1000]) {
      const query = new URLSearchParams(
        historyQuery({ search, page: 2, pageSize, grouped: false }),
      );
      assert.equal(query.get('capabilities'), 'deferred');
      assert.equal(query.get('page'), '2');
    }
  }
});
