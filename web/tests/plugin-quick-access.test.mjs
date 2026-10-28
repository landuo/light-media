import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { pluginRecentItems, pluginRememberRecent } from '../src/features/plugin-quick-access.ts';
test('fixed recent hook timestamp/order/snapshot and max3-on-add but no read truncation', () => {
  const oracle = JSON.parse(
    readFileSync(
      new URL('../../testdata/standard-navigation/frontend-oracle.json', import.meta.url),
    ),
  );
  let recent = [],
    time = 0;
  for (const id of ['a', 'b', 'c', 'd', 'b'])
    recent = pluginRememberRecent(
      recent,
      { instance_id: id, plugin_id: id, name: id, has_page: true, state: false },
      ++time,
    );
  assert.deepEqual(
    recent.map((x) => ({
      id: x.instance_id,
      plugin_name: x.name,
      has_page: x.has_page,
      state: x.state,
      plugin_id: x.plugin_id,
      access_time: x.access_time,
    })),
    oracle.recent.remembered,
  );
  const stored = [
    { id: 'low', access_time: 1 },
    { id: 'high', access_time: 9 },
    { id: 'mid', access_time: 5 },
    { id: 'four', access_time: 3 },
  ].map((x) => ({ ...x, instance_id: x.id, has_page: true }));
  assert.deepEqual(
    pluginRecentItems(JSON.stringify(stored)).map((x) => x.instance_id),
    oracle.recent.loaded.map((x) => x.id),
  );
});
