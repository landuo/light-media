import './helpers/plugin-axios-fetch-adapter.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import * as Vue from 'vue';
import {
  pluginFederationEntry,
  loadPluginAppRemote,
  pluginAppComponentCandidates,
} from '../src/features/plugin-federation.ts';
import { createPluginRemoteAPI, remotePluginAPIPath } from '../src/features/plugin-remote-api.ts';

test('standard Vue entry uses only metadata-bound immutable same-origin assets', () => {
  const descriptor = {
    instance_id: 'SeedRescue.music',
    plugin_id: 'SeedRescue',
    version: '1.0',
    package_sha256: 'a'.repeat(64),
    contract: 'light-media.vue-federation/v1',
    exposes: ['Config'],
    shared: ['vue'],
    api_contract: 'light-media.plugin-api/v1',
    entry_path: '/plugins/SeedRescue.music/frontend/' + 'a'.repeat(64) + '/assets/remoteEntry.js',
  };
  assert.equal(pluginFederationEntry(descriptor, 'Config'), '/api' + descriptor.entry_path);
  assert.throws(() => pluginFederationEntry(descriptor, 'Page'));
  assert.throws(() => pluginFederationEntry({ ...descriptor, instance_id: '../other' }, 'Config'));
  assert.throws(() =>
    pluginFederationEntry(
      { ...descriptor, entry_path: 'https://other.example/remoteEntry.js' },
      'Config',
    ),
  );
});

test('original API URLs preserve literal, runtime and explicit peer identities', () => {
  assert.equal(
    remotePluginAPIPath(
      'SeedRescue',
      'clone',
      'plugin/SeedRescue/status?view=current&page=2',
      'GET',
    ),
    '/v1/plugin/SeedRescue/status?view=current&page=2',
  );
  const hash = 'a'.repeat(40);
  assert.equal(
    remotePluginAPIPath('SeedRescue', 'clone', `plugin/SeedRescue/jobs/${hash}/download`, 'POST'),
    `/v1/plugin/SeedRescue/jobs/${hash}/download`,
  );
  assert.equal(remotePluginAPIPath('SeedRescue', 'clone', 'plugin/Other/status', 'GET'), '/v1/plugin/Other/status');
  for (const path of ['https://other.example/status', '//other.example/status'])
    assert.throws(() => remotePluginAPIPath('SeedRescue', 'clone', path, 'GET'));
  // The backend, not the frontend plugin-name list, authorizes route/method pairs.
  assert.equal(
    remotePluginAPIPath('SeedRescue', 'clone', 'plugin/SeedRescue/scan', 'DELETE'),
    '/v1/plugin/SeedRescue/scan',
  );
});

test('source risk confirmation details and requested identity survive the actual fetch adapter', async () => {
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
    assert.deepEqual(await api.post('plugin/SeedRescue/jobs/' + 'a'.repeat(40) + '/download', { candidate_id: 'candidate' }), {
      success:false, message:'请确认下载风险', data:{confirmation_required:true,candidate_id:'candidate',confirmed_download_factor:1}
    });
    assert.equal(
      calls[0].url,
      '/api/v1/plugin/SeedRescue/jobs/' + 'a'.repeat(40) + '/download',
    );
    assert.equal(calls[0].options.credentials, 'same-origin');
    assert.equal(calls[0].options.signal, controller.signal);
    assert.equal(calls[0].options.body, JSON.stringify({ candidate_id: 'candidate' }));
  } finally {
    globalThis.fetch = previous;
  }
});

test('original successful response stays unwrapped and invalid JSON remains text', async () => {
  const previous = globalThis.fetch;
  const api = createPluginRemoteAPI('SeedRescue', 'SeedRescue', new AbortController().signal);
  try {
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ success: true, data: { options: { sites: [] } } }));
    assert.deepEqual(await api.get('plugin/SeedRescue/status'), {
      success: true,
      data: { options: { sites: [] } },
    });
    globalThis.fetch = async () =>
      new Response('<html>error</html>', { headers: { 'Content-Type': 'application/json' } });
    assert.equal(await api.get('plugin/SeedRescue/status'), '<html>error</html>');
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

test('source API accepts arbitrary JSON and encodes Axios-style query params without rewriting requested identity', async (t) => {
  const previous = globalThis.fetch;
  t.after(() => {
    globalThis.fetch = previous;
  });
  const calls = [];
  const api = createPluginRemoteAPI('Demo', 'Demo_clone', new AbortController().signal);
  for (const result of [null, false, 0, 'ready', ['one', { id: 2 }]]) {
    globalThis.fetch = async (path) => {
      calls.push(path);
      return new Response(JSON.stringify(result));
    };
    assert.deepEqual(
      await api.get('plugin/Demo/history?existing=1', {
        params: { word: 'a b&c', tags: ['one', 'two'], skipped: null },
      }),
      result,
    );
    const url = new URL(calls.at(-1), 'http://owned.invalid');
    assert.equal(url.pathname, '/api/v1/plugin/Demo/history');
    assert.equal(url.searchParams.get('existing'), '1');
    assert.equal(url.searchParams.get('word'), 'a b&c');
    assert.deepEqual(url.searchParams.getAll('tags[]'), ['one', 'two']);
    assert.equal(url.searchParams.has('skipped'), false);
  }
  globalThis.fetch = async () => new Response('null', { status: 500 });
  await assert.rejects(api.get('plugin/Demo/history'), /500/);
});

test('full-page source component sequence tries custom nav component then AppPage then Page', async () => {
  assert.deepEqual(pluginAppComponentCandidates('MAIN'), ['AppPage', 'Page']);
  assert.deepEqual(pluginAppComponentCandidates('my_tool'), ['AppPageMyTool', 'AppPage', 'Page']);
  const calls = [],
    result = { component: { setup() {} }, descriptor: { instance_id: 'clone' } };
  const loader = async (instance, name) => {
    calls.push([instance, name]);
    if (name !== 'Page') throw new Error('missing ' + name);
    return result;
  };
  assert.equal(await loadPluginAppRemote('clone', 'my_tool', {}, loader), result);
  assert.deepEqual(calls, [
    ['clone', 'AppPageMyTool'],
    ['clone', 'AppPage'],
    ['clone', 'Page'],
  ]);
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(
    loadPluginAppRemote('clone', 'main', { signal: controller.signal }, loader),
    { name: 'AbortError' },
  );
  assert.equal(calls.length, 3);
});

test('custom APIs preserve plain text and text errors without decoding binary or retrying writes', async (t) => {
  const previous = globalThis.fetch;
  t.after(() => {
    globalThis.fetch = previous;
  });
  const api = createPluginRemoteAPI('Demo', 'Demo_clone', new AbortController().signal);
  let requests = 0;
  globalThis.fetch = async () => {
    requests++;
    return new Response('已生成报告\n第二行', {
      headers: { 'Content-Type': 'text/plain; charset=utf-8' },
    });
  };
  assert.equal(await api.get('plugin/Demo/report'), '已生成报告\n第二行');
  globalThis.fetch = async () => {
    requests++;
    return new Response('owned refusal', {
      status: 400,
      headers: { 'Content-Type': 'text/plain' },
    });
  };
  await assert.rejects(api.post('plugin/Demo/write', {}), e => e.response.status===400 && e.response.data==='owned refusal');
  assert.equal(requests, 2, 'failed business writes are never retried');
  let decoded = false;
  globalThis.fetch = async () => ({
    ok: true,
    status: 200,
    headers: new Headers({ 'Content-Type': 'application/octet-stream' }),
    text: async () => {
      decoded = true;
      return 'corrupted bytes';
    },
    json: async () => {
      decoded = true;
      return {};
    },
  });
  assert.equal(await api.get('plugin/Demo/file'), 'corrupted bytes');
  assert.equal(decoded, true, 'fixed Axios defaults to a text response unless responseType is explicit');
});

test('explicit Axios binary response types preserve bytes and preserve upload request identity', async (t) => {
  const previous = globalThis.fetch;
  t.after(() => {globalThis.fetch = previous;});
  const api = createPluginRemoteAPI('Demo', 'Demo_clone', new AbortController().signal);
  const bytes = new Uint8Array([255, 0, 1, 254, 10]);
  const calls=[];
  globalThis.fetch = async (url,options) => {
    calls.push({url,options});
    return new Response(bytes,{headers:{'Content-Type':'application/octet-stream','Content-Disposition':'attachment; filename="owned.bin"'}});
  };
  const blob = await api.get('plugin/Demo/file', {responseType:'blob'});
  assert.ok(blob instanceof Blob);
  assert.equal(blob.type,'application/octet-stream');
  assert.deepEqual(new Uint8Array(await blob.arrayBuffer()),bytes);
  const buffer = await api.post('plugin/Demo/upload',bytes,{responseType:'arraybuffer',headers:{'X-Correlation-ID':'owned'}});
  assert.deepEqual(new Uint8Array(buffer),bytes);
  assert.equal(calls[1].url,'/api/v1/plugin/Demo/upload');
  assert.deepEqual(new Uint8Array(calls[1].options.body),bytes);
  assert.equal(calls[1].options.headers.get('Content-Type'),'application/x-www-form-urlencoded');
  assert.equal(calls[1].options.headers.get('X-Correlation-ID'),'owned');
  globalThis.fetch=async()=>new Response('{"success":false,"message":"owned binary failure"}',{status:503,headers:{'Content-Type':'application/json'}});
  await assert.rejects(api.get('plugin/Demo/file',{responseType:'blob'}),e => e.response.status===503 && e.response.data instanceof Blob);
  assert.equal(calls.length,2,'failed binary writes are not retried');
});
