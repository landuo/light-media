import test from 'node:test';
import assert from 'node:assert/strict';
import { jobQueueInfo } from '../src/features/job-queue.ts';

test('queue presents the actual object, paths, stage and last recorded activity', () => {
  const summary = {
    type_label: '整理文件',
    title: '整理文件 · 诛仙 第 1 季 第 3 集',
    subject: '诛仙 第 1 季 第 3 集',
    stage: '等待刮削元数据',
    source: '/media/download/Zhu.Xian.S01E03.mkv',
    target: '/media/国漫/诛仙/Season 01/诛仙 - S01E03.mkv',
    file_count: 1,
    activity: '视频已发布，已加入刮削任务',
    activity_at: '2026-10-15T10:00:00Z',
  };
  const info = jobQueueInfo({ kind: 'import', state: 'running', summary });
  assert.equal(info.title, summary.title);
  assert.equal(info.subject, summary.subject);
  assert.equal(info.showSubject, false);
  assert.equal(info.source, summary.source);
  assert.equal(info.target, summary.target);
  assert.equal(info.stage, summary.stage);
  assert.equal(info.extra, '1 个文件');
  assert.equal(info.activity, summary.activity);
  assert.equal(info.activityAt, summary.activity_at);
});

test('download metadata distinguishes a resource task from its downloader', () => {
  const info = jobQueueInfo({
    kind: 'native_download',
    state: 'running',
    summary: { title: '下载资源 · Joker 2024', subject: 'Joker 2024', downloader: '客厅 qB' },
  });
  assert.equal(info.typeLabel, '下载资源');
  assert.equal(info.extra, '客厅 qB');
  assert.equal(info.fileCount, undefined);
  assert.equal(info.stage, '正在执行，暂无阶段记录');
});

test('old or missing associations stay explicit without guessing from IDs or payloads', () => {
  const info = jobQueueInfo({ kind: 'subscription', state: 'queued' });
  assert.equal(info.title, '检查订阅资源');
  assert.equal(info.subject, '关联资料不可用');
  assert.equal(info.showSubject, true);
  assert.equal(info.stage, '等待执行');
  assert.equal(info.activity, '');
  assert.equal(info.activityAt, '');
  assert.equal(info.extra, '');
  assert.equal(jobQueueInfo({ kind: 'future_kind', state: 'unknown' }).title, '后台任务');
});

test('partial summaries preserve useful subjects and suppress blank or invalid metadata', () => {
  const info = jobQueueInfo({
    kind: 'import',
    state: 'failed',
    summary: { title: ' ', subject: 'Joker 2024', source: ' ', file_count: -1, downloader: ' ' },
  });
  assert.equal(info.title, '整理文件');
  assert.equal(info.subject, 'Joker 2024');
  assert.equal(info.showSubject, true);
  assert.equal(info.stage, '执行失败');
  assert.equal(info.source, '');
  assert.equal(info.extra, '');
});
