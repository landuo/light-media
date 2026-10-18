import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import * as Vue from 'vue';
import * as Vuetify from 'vuetify';
import {
  pluginFederationEntry,
  pluginRemoteSharedScope,
} from '../src/features/plugin-federation.ts';
import { createPluginRemoteAPI, remotePluginAPIPath } from '../src/features/plugin-remote-api.ts';
import { pluginNavigationTarget } from '../src/features/plugin-navigation.ts';

const pluginID = 'BrushFlowLanduo';
const sourceBase = 'plugin/' + pluginID + '/';

test('BrushFlow original API contract binds every route to the displayed instance', () => {
  for (const [method, path] of [
    ['GET', 'status'],
    ['POST', 'settings'],
    ['POST', 'tasks'],
    ['GET', 'tasks/task-42?state=deleted&page=2&page_size=100'],
    ['PUT', 'tasks/task-42'],
    ['DELETE', 'tasks/task-42'],
    ['POST', 'tasks/task-42/state'],
    ['POST', 'tasks/task-42/run'],
    ['POST', 'tasks/task-42/check'],
    ['POST', 'tasks/task-42/clear'],
  ]) {
    assert.equal(
      remotePluginAPIPath(pluginID, 'BrushFlowLanduo.second', sourceBase + path, method),
      '/plugins/BrushFlowLanduo.second/api/' + path,
    );
  }
  for (const id of ['legacy-id', '任务1', '%E4%BB%BB%E5%8A%A11']) {
    assert.equal(
      remotePluginAPIPath(pluginID, 'clone', sourceBase + 'tasks/' + id, 'DELETE'),
      '/plugins/clone/api/tasks/' + id,
    );
  }
  for (const [method, path] of [
    ['POST', 'status'],
    ['GET', 'settings'],
    ['GET', 'tasks'],
    ['POST', 'tasks/a'],
    ['DELETE', 'tasks/a?state=all'],
    ['GET', 'tasks/a/run'],
    ['POST', 'tasks/a/delete'],
    ['POST', 'tasks/a/run?plugin=other'],
    ['GET', 'tasks/../status'],
    ['GET', 'tasks/%2e%2e'],
    ['PUT', 'tasks/%2Fother'],
    ['GET', 'tasks/%5cother'],
    ['GET', 'tasks/%00'],
    ['GET', 'tasks/%'],
    ['GET', 'tasks/a#other'],
    ['GET', 'tasks/a?state=all#other'],
    ['GET', 'status\n'],
  ]) {
    assert.throws(() => remotePluginAPIPath(pluginID, 'clone', sourceBase + path, method));
  }
  for (const path of [
    'https://other.example/' + sourceBase + 'status',
    '/api/' + sourceBase + 'status',
    'plugin/SeedRescue/status',
  ])
    assert.throws(() => remotePluginAPIPath(pluginID, 'clone', path, 'GET'));
  assert.throws(() => remotePluginAPIPath(pluginID, '../other', sourceBase + 'status', 'GET'));
});

test('BrushFlow mutation methods keep source payloads and only refresh after explicit success', async () => {
  const previous = globalThis.fetch;
  const controller = new AbortController();
  const calls = [];
  let changes = 0;
  let result = { success: true, data: { id: 'task-42' } };
  let status = 200;
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    return new Response(JSON.stringify(result), { status });
  };
  try {
    const api = createPluginRemoteAPI(pluginID, 'clone', controller.signal, () => {
      changes++;
    });
    const task = { id: 'task-42', name: '原版任务', enabled: true, sites: [1, 2] };
    assert.deepEqual(await api.put(sourceBase + 'tasks/task-42', task), result);
    assert.equal(changes, 1);
    assert.equal(calls[0].url, '/api/plugins/clone/api/tasks/task-42');
    assert.equal(calls[0].options.method, 'PUT');
    assert.equal(calls[0].options.body, JSON.stringify(task));
    assert.equal(calls[0].options.credentials, 'same-origin');
    assert.equal(calls[0].options.signal, controller.signal);
    await api.delete(sourceBase + 'tasks/task-42');
    assert.equal(changes, 2);
    assert.equal(calls[1].options.method, 'DELETE');
    assert.equal(calls[1].options.body, undefined);
    await api.get(sourceBase + 'status');
    assert.equal(changes, 2);
    result = { success: false, message: '任务正在执行', data: { task_id: 'task-42' } };
    await assert.rejects(
      api.post(sourceBase + 'tasks/task-42/run', {}),
      (error) => error.message === '任务正在执行' && error.details.task_id === 'task-42',
    );
    assert.equal(changes, 2);
    result = { success: true };
    status = 500;
    await assert.rejects(api.post(sourceBase + 'settings', {}));
    assert.equal(changes, 2);
    status = 200;
    result = { data: {} };
    await api.post(sourceBase + 'settings', {});
    assert.equal(changes, 2);
    result = { success: true, data: { enabled: true } };
    const refreshFails = createPluginRemoteAPI(pluginID, 'clone', controller.signal, () =>
      Promise.reject(new Error('sidebar temporarily unavailable')),
    );
    assert.deepEqual(await refreshFails.post(sourceBase + 'settings', { enabled: true }), result);
    const refreshThrows = createPluginRemoteAPI(pluginID, 'clone', controller.signal, () => {
      throw new Error('card temporarily unavailable');
    });
    assert.deepEqual(await refreshThrows.post(sourceBase + 'settings', { enabled: true }), result);
  } finally {
    globalThis.fetch = previous;
  }
});

test('BrushFlow ten unchanged assets load all four original exports with actual Vue and Vuetify', async () => {
  const base = new URL('../../internal/plugins/brushflow/', import.meta.url);
  const manifest = JSON.parse(readFileSync(new URL('reference/frontend-manifest.json', base)));
  assert.equal(manifest.files.length, 10);
  for (const asset of manifest.files) {
    const bytes = readFileSync(new URL('frontend/' + asset.path, base));
    assert.equal(bytes.length, asset.bytes, asset.path);
    assert.equal(createHash('sha256').update(bytes).digest('hex'), asset.sha256, asset.path);
  }
  const scope = pluginRemoteSharedScope(pluginID);
  assert.equal((await scope.vue[Vue.version].get())(), Vue);
  assert.equal((await scope.vuetify[Vuetify.version].get())(), Vuetify);
  assert.equal(pluginRemoteSharedScope('SeedRescue').vuetify, undefined);
  assert.throws(() => pluginRemoteSharedScope('Unknown'));
  const previousDocument = globalThis.document;
  const previousScope = globalThis.__federation_shared__;
  const links = [];
  globalThis.document = {
    createElement: () => ({}),
    head: { appendChild: (value) => links.push(value) },
  };
  try {
    const entry = await import(new URL('frontend/assets/remoteEntry.js', base));
    entry.init(scope);
    for (const name of ['Config', 'Page', 'Dashboard', 'AppPage']) {
      const component = (await entry.get('./' + name))();
      assert.equal(typeof component.setup, 'function', name);
      assert.ok(component.props.api, name);
      if (name === 'Config') {
        assert.ok(component.props.initialConfig);
        assert.deepEqual(component.emits, ['layout', 'close']);
      }
      if (name === 'Page') assert.deepEqual(component.emits, ['action', 'close']);
      if (name === 'AppPage') assert.deepEqual(component.emits, ['action']);
      if (name === 'Dashboard') assert.ok(component.props.config);
    }
    assert.ok(links.some((link) => link.href.endsWith('/assets/BrushFlowWorkbench-Cm4ufV31.css')));
    assert.equal(globalThis.__federation_shared__.default.vuetify[Vuetify.version].loaded, true);
  } finally {
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
    if (previousScope === undefined) delete globalThis.__federation_shared__;
    else globalThis.__federation_shared__ = previousScope;
  }
});

test('BrushFlow full-page navigation retains the displayed clone and original main nav key', () => {
  assert.equal(
    pluginNavigationTarget({ plugin_id: pluginID, instance_id: 'brush.2', nav_key: 'main' }),
    'plugin-app/brush.2/main',
  );
  for (const name of ['Config', 'Page', 'Dashboard', 'AppPage'])
    assert.equal(
      pluginFederationEntry(pluginID, 'brush.2', name),
      '/api/plugins/brush.2/frontend/assets/remoteEntry.js',
    );
});
