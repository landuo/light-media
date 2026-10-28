import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  normalizePluginLogLevel,
  pluginLogEntry,
  pluginLogEntries,
  appendPluginLogs,
  groupPluginLogs,
  filterPluginLogGroups,
} from '../src/features/plugin-logs.ts';

const source = JSON.parse(
  readFileSync(
    new URL('../../testdata/plugins/logging-original-oracle.json', import.meta.url),
    'utf8',
  ),
);

test('original logging normalization, burst grouping and filters match actual source references', () => {
  for (const item of source.levels)
    assert.equal(normalizePluginLogLevel(item.input), item.expected);
  for (const item of source.groups)
    assert.deepEqual(groupPluginLogs(item.entries), item.expected, item.name);
  for (const item of source.filters)
    assert.deepEqual(
      filterPluginLogGroups(item.groups, item.level, item.query),
      item.expected,
      item.level + ':' + item.query,
    );
});

test('structured Go log adaptation retains zero timestamps, literal text and concrete instance scope', () => {
  const input = {
    id: 'log_one',
    plugin_instance_id: 'one',
    level: 'WARN',
    time: '1970-01-01T00:00:00Z',
    module: 'plugins',
    message: '\u001b[31m你好 <img src=x onerror=bad()>\u001b[0m',
    error_code: 'SOURCE_ERROR',
  };
  const entry = pluginLogEntry(input, 'one');
  assert.equal(entry.level, 'WARNING');
  assert.equal(entry.timestampMs, 0);
  assert.equal(entry.message, '你好 <img src=x onerror=bad()> [SOURCE_ERROR]');
  assert.equal(entry.raw.includes('\u001b'), false);
  assert.equal(pluginLogEntry(input, 'two'), null);
  for (const mutation of [
    { id: '' },
    { id: 'x'.repeat(101) },
    { message: 0 },
    { message: 'x'.repeat(16385) },
  ])
    assert.equal(pluginLogEntry({ ...input, ...mutation }, 'one'), null);
  for (const value of [null, [], 0, 'text']) assert.equal(pluginLogEntry(value, 'one'), null);
});

test('history and resumed stream deduplicate stable IDs within a bounded 600-line display', () => {
  const record = (id) =>
    pluginLogEntry(
      {
        id: 'log_' + id,
        plugin_instance_id: 'one',
        level: 'INFO',
        time: '2026-10-16T00:00:00Z',
        message: 'fixture ' + id,
      },
      'one',
    );
  const history = Array.from({ length: 50 }, (_, i) => record(i));
  const added = appendPluginLogs(history, [record(49), record(50), record(50), record(51)]);
  assert.equal(added.count, 2);
  assert.equal(added.entries.length, 52);
  const bounded = appendPluginLogs(
    added.entries,
    Array.from({ length: 700 }, (_, i) => record(i + 52)),
  );
  assert.equal(bounded.count, 700);
  assert.equal(bounded.entries.length, 600);
  assert.equal(bounded.entries[0].id, 'log_152');
  assert.equal(bounded.entries.at(-1).id, 'log_751');
});

test('fixed physical-line parser, continuation metadata and 600-line bound are retained', () => {
  const oracle = JSON.parse(
    readFileSync(
      new URL('../../testdata/standard-plugin-logs/source-oracle.json', import.meta.url),
    ),
  );
  for (const sample of oracle.cases) {
    const records = sample.messages.flatMap((message, index) =>
      pluginLogEntries(
        {
          id: 'owned_' + index,
          plugin_instance_id: 'one',
          level: 'INFO',
          time: new Date('2026-10-26 12:00:00.001').toISOString(),
          module: 'plugins',
          message,
          ...(sample.raw_messages ? { raw: sample.raw_messages[index] } : {}),
        },
        'one',
      ),
    );
    const actual = appendPluginLogs([], records);
    const projection = (item) => ({
      level: item.level,
      message: item.message,
      source: item.source,
      appName: item.appName || '',
      timestampMs: item.timestampMs,
      secondKey: item.secondKey,
      secondDisplay: item.secondDisplay,
      timeDisplay: item.timeDisplay,
    });
    assert.deepEqual(
      actual.entries.map(projection),
      sample.expected.entries.map(projection),
      sample.name,
    );
    assert.equal(actual.count, sample.expected.pending, sample.name);
    assert.deepEqual(
      appendPluginLogs(actual.entries, actual.entries).entries,
      actual.entries,
      'stable line identities survive resumed overlap',
    );
  }
});
