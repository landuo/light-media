import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createPluginNavigation,
  pluginNavigationTarget,
} from '../src/features/plugin-navigation.ts';

test('sidebar caches successful empty results and merges concurrent requests', async () => {
  let resolve,
    calls = 0;
  const navigation = createPluginNavigation(() => {
    calls++;
    return new Promise((done) => {
      resolve = done;
    });
  });
  const first = navigation.ensure(),
    second = navigation.ensure();
  assert.equal(first, second);
  assert.equal(calls, 1);
  resolve([]);
  await first;
  await navigation.ensure();
  assert.equal(navigation.state.loaded, true);
  assert.deepEqual(navigation.state.items, []);
  assert.equal(calls, 1);
});

test('sidebar retries once after failure and never caches a failed result', async () => {
  let calls = 0;
  const navigation = createPluginNavigation(async () => {
    calls++;
    throw new Error('unavailable');
  });
  await navigation.ensure();
  assert.equal(calls, 2);
  assert.equal(navigation.state.loaded, false);
  await navigation.ensure();
  assert.equal(calls, 4);
  assert.equal(navigation.state.loaded, false);
});

test('logout invalidates old in-flight results without discarding the new session request', async () => {
  const requests = [];
  const navigation = createPluginNavigation(() => new Promise((resolve) => requests.push(resolve)));
  const first = navigation.ensure();
  navigation.reset();
  const second = navigation.ensure();
  requests[0]([{ title: 'old session' }]);
  await first;
  assert.deepEqual(navigation.state.items, []);
  assert.equal(navigation.ensure(), second);
  requests[1]([{ title: 'new session' }]);
  await second;
  assert.equal(navigation.state.items[0].title, 'new session');
  navigation.reset();
  assert.deepEqual(navigation.state.items, []);
  assert.equal(navigation.state.loaded, false);
});

test('sidebar full-page route preserves the chosen clone and source nav key', () => {
  assert.equal(
    pluginNavigationTarget({
      plugin_id: 'SeedRescue',
      instance_id: 'SeedRescue2',
      nav_key: 'main',
    }),
    'plugin-app/SeedRescue2/main',
  );
  for (const item of [
    { plugin_id: 'Unknown', instance_id: 'SeedRescue', nav_key: 'main' },
    { plugin_id: 'SeedRescue', instance_id: '../other', nav_key: 'main' },
    { plugin_id: 'SeedRescue', instance_id: 'SeedRescue', nav_key: '../../settings' },
  ])
    assert.throws(() => pluginNavigationTarget(item));
});
