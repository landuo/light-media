import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import * as Vue from 'vue';
import { pluginFederationEntry } from '../src/features/plugin-federation.ts';
import { createPluginRemoteAPI, remotePluginAPIPath } from '../src/features/plugin-remote-api.ts';

test('original Vue entry uses only fixed same-origin assets and the selected installed instance', () => {
  assert.equal(
    pluginFederationEntry('SeedRescue', 'SeedRescue.music', 'Config'),
    '/api/plugins/SeedRescue.music/frontend/assets/remoteEntry.js',
  );
  for (const args of [
    ['Unknown', 'SeedRescue', 'Config'],
    ['SeedRescue', '../other', 'Page'],
    ['SeedRescue', 'SeedRescue', 'https://other.example/entry.js'],
  ])
    assert.throws(() => pluginFederationEntry(...args));
});

test('original API URLs bind to the displayed instance without allowing other plugin APIs', () => {
  assert.equal(
    remotePluginAPIPath(
      'SeedRescue',
      'clone',
      'plugin/SeedRescue/status?view=current&page=2',
      'GET',
    ),
    '/plugins/clone/api/status?view=current&page=2',
  );
  const hash = 'a'.repeat(40);
  assert.equal(
    remotePluginAPIPath('SeedRescue', 'clone', `plugin/SeedRescue/jobs/${hash}/download`, 'POST'),
    `/plugins/clone/api/jobs/${hash}/download`,
  );
  for (const path of [
    'https://other.example/status',
    'plugin/Other/status',
    'plugin/SeedRescue/../config',
    'plugin/SeedRescue/status#other',
    'plugin/SeedRescue/status\n',
  ])
    assert.throws(() => remotePluginAPIPath('SeedRescue', 'clone', path, 'GET'));
  assert.throws(() => remotePluginAPIPath('SeedRescue', 'clone', 'plugin/SeedRescue/scan', 'GET'));
  assert.throws(() =>
    remotePluginAPIPath('SeedRescue', 'clone', 'plugin/SeedRescue/scan', 'DELETE'),
  );
});

test('source risk confirmation details and instance binding survive the actual fetch adapter', async () => {
  const previous = globalThis.fetch;
  const controller = new AbortController();
  const calls = [];
  globalThis.fetch = async (url, options) => {
    calls.push({ url, options });
    return new Response(
      JSON.stringify({
        success: false,
        message: '请确认下载风险',
        data: {
          confirmation_required: true,
          candidate_id: 'candidate',
          confirmed_download_factor: 1,
        },
      }),
    );
  };
  try {
    const api = createPluginRemoteAPI('SeedRescue', 'second-instance', controller.signal);
    await assert.rejects(
      api.post('plugin/SeedRescue/jobs/' + 'a'.repeat(40) + '/download', {
        candidate_id: 'candidate',
      }),
      (error) =>
        error.message === '请确认下载风险' &&
        error.details.confirmation_required &&
        error.details.candidate_id === 'candidate',
    );
    assert.equal(
      calls[0].url,
      '/api/plugins/second-instance/api/jobs/' + 'a'.repeat(40) + '/download',
    );
    assert.equal(calls[0].options.credentials, 'same-origin');
    assert.equal(calls[0].options.signal, controller.signal);
    assert.equal(calls[0].options.body, JSON.stringify({ candidate_id: 'candidate' }));
  } finally {
    globalThis.fetch = previous;
  }
});

test('original successful response stays unwrapped and invalid JSON cannot fabricate success', async () => {
  const previous = globalThis.fetch;
  const api = createPluginRemoteAPI('SeedRescue', 'SeedRescue', new AbortController().signal);
  try {
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ success: true, data: { options: { sites: [] } } }));
    assert.deepEqual(await api.get('plugin/SeedRescue/status'), {
      success: true,
      data: { options: { sites: [] } },
    });
    globalThis.fetch = async () => new Response('<html>error</html>');
    await assert.rejects(api.get('plugin/SeedRescue/status'), /无法读取/);
  } finally {
    globalThis.fetch = previous;
  }
});

test('all eight original assets load their four actual federation exports with the app Vue module', async () => {
  const base = new URL('../../internal/plugins/seedrescue/', import.meta.url);
  const manifest = JSON.parse(readFileSync(new URL('reference/frontend-manifest.json', base)));
  for (const asset of manifest.files) {
    const bytes = readFileSync(new URL('frontend/' + asset.path, base));
    assert.equal(createHash('sha256').update(bytes).digest('hex'), asset.sha256, asset.path);
  }
  assert.equal(manifest.files.length, 8);
  const previousDocument = globalThis.document,
    previousScope = globalThis.__federation_shared__;
  const links = [];
  globalThis.document = {
    createElement: () => ({}),
    head: { appendChild: (value) => links.push(value) },
  };
  try {
    const entry = await import(new URL('frontend/assets/remoteEntry.js', base));
    entry.init({
      vue: { [Vue.version]: { get: async () => () => Vue, loaded: true, scope: 'default' } },
    });
    for (const name of ['Config', 'Page', 'Dashboard', 'AppPage']) {
      const component = (await entry.get('./' + name))();
      assert.equal(typeof component.setup, 'function', name);
      assert.ok(name === 'Dashboard' ? component.props.config : component.props.api, name);
      if (name === 'Config') {
        assert.ok(component.props.initialConfig);
        assert.ok(component.emits.includes('save'));
      }
    }
    assert.ok(
      links.some((link) => link.href.endsWith('/frontend/assets/SeedRescueWorkbench-C0MKqaDa.css')),
    );
    assert.equal(globalThis.__federation_shared__.default.vue[Vue.version].loaded, true);
  } finally {
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
    if (previousScope === undefined) delete globalThis.__federation_shared__;
    else globalThis.__federation_shared__ = previousScope;
  }
});
