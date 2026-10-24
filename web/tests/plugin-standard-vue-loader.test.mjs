import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { pathToFileURL } from 'node:url';
import * as Vue from 'vue';
import * as Vuetify from 'vuetify';
import { createPluginRemoteLoader, pluginRemoteSharedScope } from '../src/features/plugin-federation.ts';
import { parsePluginFrontendDescriptor } from '../src/features/plugin-remote-contract.ts';
import { createPluginRemoteAPI, remotePluginAPIPath } from '../src/features/plugin-remote-api.ts';

const digest = 'a'.repeat(64);
function descriptor(instance = 'Fixture.clone', version = '1.0', sha = digest) {
  return { instance_id: instance, plugin_id: 'NotAHostBuiltin', version,
    package_sha256: sha, contract: 'light-media.vue-federation/v1',
    exposes: ['Config', 'Page', 'Dashboard', 'AppPage'], shared: ['vue', 'vuetify'],
    api_contract: 'light-media.plugin-api/v1',
    entry_path: '/plugins/' + instance + '/frontend/' + sha + '/assets/remoteEntry.js' };
}
const response = (data, status = 200) => new Response(JSON.stringify({ data }), { status });
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };
const entry = () => ({ init() {}, async get(name) { return () => ({ name, setup: () => ({ fixture: true }) }); } });

test('actual JavaScript federation graph resolves all four exports under instance/digest with shared host modules', async (t) => {
  const root = mkdtempSync(join(tmpdir(), 'standard-vue-graph-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const meta = descriptor();
  const path = join(root, meta.entry_path.slice(1));
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(join(root, 'package.json'), JSON.stringify({ type: 'module' }));
  writeFileSync(join(dirname(path), 'style.css'), '.fixture { color: inherit; }');
  writeFileSync(join(dirname(path), 'component.js'), `export function make(name, vue, css) {
    return vue.defineComponent({ name, props: ['api', 'initialConfig', 'navKey', 'config'],
      setup(props) { return { observedNavKey: props.navKey, css, count: vue.ref(0) }; } });
  }`);
  writeFileSync(path, `import { make } from './component.js'; let scope;
    export function init(value) { scope = value; }
    export async function get(name) {
      const versions = Object.values(scope.vue); const vue = (await versions[0].get())();
      return () => make(name.slice(2), vue, new URL('./style.css', import.meta.url).href);
    }`);
  const calls = [], imports = [];
  const load = createPluginRemoteLoader({
    fetcher: async (url, options) => { calls.push({ url, options }); return response(meta); },
    importEntry: async (url) => { imports.push(url); return import(pathToFileURL(join(root, url.slice('/api/'.length))).href); },
  });
  for (const name of meta.exposes) {
    const { component, descriptor: loaded } = await load(meta.instance_id, name, { version: '1.0' });
    const state = component.setup({ navKey: 'not-main' });
    assert.equal(component.name, name);
    assert.equal(state.observedNavKey, 'not-main');
    assert.equal(Vue.isRef(state.count), true);
    assert.match(state.css, new RegExp('/' + digest + '/assets/style.css$'));
    assert.equal(loaded.plugin_id, 'NotAHostBuiltin');
  }
  assert.equal(imports.length, 1);
  assert.equal(calls.length, 4, 'metadata is never indefinitely cached');
  assert.equal(calls[0].options.credentials, 'same-origin');
  assert.equal(calls[0].options.cache, 'no-store');
  assert.equal(calls[0].options.headers, undefined, 'no API key or token is injected');
  const shared = pluginRemoteSharedScope(['vue', 'vuetify']);
  assert.equal((await shared.vue[Vue.version].get())(), Vue);
  assert.equal((await shared.vuetify[Vuetify.version].get())(), Vuetify);
});

test('unsupported or malformed descriptors fail before any module import', async () => {
  const invalid = [
    { instance_id: 'other' }, { plugin_id: '../other' }, { version: '' }, { package_sha256: 'x' },
    { contract: 'unknown/v2' }, { api_contract: 'unknown/v2' }, { exposes: ['Page'] },
    { exposes: ['Config', 'Config'] }, { exposes: { Config: './Config' } }, { exposes: ['Unknown'] },
    { shared: [] }, { shared: ['vuetify'] }, { shared: ['vue', 'react'] }, { shared: ['vue', 'vue'] },
    { entry_path: 'https://evil.test/remoteEntry.js' },
    { entry_path: descriptor().entry_path + '?package=other' },
    { entry_path: descriptor().entry_path.replace(digest, 'b'.repeat(64)) },
    { entry_path: descriptor().entry_path.replace('/assets/', '/assets/../') },
    { entry_path: descriptor().entry_path.replace('/assets/', '/assets/%2e%2e/') },
    { entry_path: descriptor().entry_path.replace('Fixture.clone', 'Other.clone') },
  ];
  for (const changes of invalid) {
    let imports = 0;
    const load = createPluginRemoteLoader({ fetcher: async () => response({ ...descriptor(), ...changes }), importEntry: async () => { imports++; return entry(); } });
    await assert.rejects(load('Fixture.clone', 'Config'));
    assert.equal(imports, 0, JSON.stringify(changes));
  }
  const nested = descriptor(); nested.entry_path = nested.entry_path.replace('/assets/', '/assets/build/');
  assert.equal(parsePluginFrontendDescriptor(nested, nested.instance_id, 'Config').entry_path, nested.entry_path);
});

test('updated package and distinct clones use separate immutable entry graphs; stale versions fail closed', async () => {
  let meta = descriptor();
  const imports = [];
  const load = createPluginRemoteLoader({ fetcher: async () => response(meta), importEntry: async (url) => { imports.push(url); return entry(); } });
  const first = await load(meta.instance_id, 'Page', { version: '1.0' });
  meta = descriptor('Fixture.clone', '2.0', 'b'.repeat(64));
  await assert.rejects(load(meta.instance_id, 'Page', { version: '1.0' }), /版本已变化/);
  await load(meta.instance_id, 'Page', { version: '2.0' });
  meta = descriptor('Another.clone', '1.0', digest);
  await load(meta.instance_id, 'Page', { version: '1.0' });
  assert.equal(imports.length, 3);
  assert.equal(new Set(imports).size, 3);
  assert.equal(first.descriptor.package_sha256, digest);
  assert.ok(Object.isFrozen(first.descriptor));
});

test('proxy base path is honored without changing descriptor source identity', async () => {
  const previous = globalThis.location;
  globalThis.location = { pathname: '/light-media/' };
  const calls = [];
  try {
    const load = createPluginRemoteLoader({ fetcher: async (url) => { calls.push(url); return response(descriptor()); }, importEntry: async (url) => { calls.push(url); return entry(); } });
    const result = await load('Fixture.clone', 'Page');
    assert.equal(calls[0], '/light-media/api/plugins/Fixture.clone/frontend');
    assert.equal(calls[1], '/light-media/api' + descriptor().entry_path);
    assert.equal(result.descriptor.plugin_id, 'NotAHostBuiltin');
  } finally { if (previous === undefined) delete globalThis.location; else globalThis.location = previous; }
});

test('concurrent consumers share one initialized entry but cancel independently', async () => {
  const pending = deferred(); let imports = 0, initialized = 0, factories = 0;
  const load = createPluginRemoteLoader({ fetcher: async () => response(descriptor()), importEntry: async () => { imports++; return pending.promise; } });
  const controller = new AbortController();
  const first = load('Fixture.clone', 'Config', { signal: controller.signal });
  const rejected = assert.rejects(first, { name: 'AbortError' });
  const second = load('Fixture.clone', 'Page');
  await new Promise(setImmediate);
  controller.abort();
  pending.resolve({ init() { initialized++; }, async get() { return () => { factories++; return { setup() {} }; }; } });
  await rejected; await second;
  assert.equal(imports, 1); assert.equal(initialized, 1); assert.equal(factories, 1);
});

test('cancel before metadata and after get prevents late component factory execution', async () => {
  for (const stage of ['metadata', 'get']) {
    const pending = deferred(), controller = new AbortController(); let factories = 0, imports = 0;
    const load = createPluginRemoteLoader({
      fetcher: async () => stage === 'metadata' ? pending.promise : response(descriptor()),
      importEntry: async () => { imports++; return { init() {}, async get() { await pending.promise; return () => { factories++; return {}; }; } }; },
    });
    const running = load('Fixture.clone', 'Config', { signal: controller.signal });
    const rejected = assert.rejects(running, { name: 'AbortError' });
    await new Promise(setImmediate); controller.abort(); await rejected;
    pending.resolve(response(descriptor())); await new Promise(setImmediate);
    assert.equal(factories, 0); if (stage === 'metadata') assert.equal(imports, 0);
  }
});

test('timeout does not publish a late factory and failed initialization can retry', async () => {
  const pending = deferred(); let factories = 0;
  const load = createPluginRemoteLoader({ timeoutMs: 5, fetcher: async () => response(descriptor()), importEntry: async () => pending.promise });
  await assert.rejects(load('Fixture.clone', 'Config'), /超时/);
  pending.resolve({ init() {}, async get() { return () => { factories++; return {}; }; } });
  await new Promise(setImmediate); assert.equal(factories, 0);
  let attempts = 0;
  const retry = createPluginRemoteLoader({ fetcher: async () => response(descriptor()), importEntry: async () => { attempts++; if (attempts === 1) throw new Error('fixture import failed'); return entry(); } });
  await assert.rejects(retry('Fixture.clone', 'Config'), /fixture import failed/);
  await retry('Fixture.clone', 'Config'); assert.equal(attempts, 2);
});

for (const [label, module] of [['entry', {}], ['factory', { init() {}, get: async () => 4 }], ['component', { init() {}, get: async () => () => null }]]) {
  test('invalid federation ' + label + ' is an explicit error', async () => {
    const load = createPluginRemoteLoader({ fetcher: async () => response(descriptor()), importEntry: async () => module });
    await assert.rejects(load('Fixture.clone', 'Config'), /无效/);
  });
}

test('generic API leaves method authorization to backend, preserves refusal, binds clone and never injects credentials', async () => {
  const previous = globalThis.fetch; const calls = [], controller = new AbortController(); let changed = 0;
  globalThis.fetch = async (url, options) => { calls.push({ url, options }); return new Response(JSON.stringify({ error: { message: 'route denied' } }), { status: 403 }); };
  try {
    const api = createPluginRemoteAPI('NotAHostBuiltin', 'Fixture.clone', controller.signal, () => changed++);
    await assert.rejects(api.patch('plugin/NotAHostBuiltin/custom/42', { enabled: true }), /route denied/);
    assert.equal(changed, 0); assert.equal(calls[0].url, '/api/plugins/Fixture.clone/api/custom/42');
    assert.deepEqual(calls[0].options.headers, { 'Content-Type': 'application/json' });
    assert.equal(calls[0].options.signal, controller.signal);
    assert.equal(calls[0].options.redirect, 'error');
    for (const path of ['plugin/Other/x', '/api/plugins/Other/api/x', 'https://evil.test/x', 'plugin/NotAHostBuiltin/%2e%2e/x', 'plugin/NotAHostBuiltin/%252e%252e/x', 'plugin/NotAHostBuiltin/x%2Fy', 'plugin/NotAHostBuiltin/x//y', 'plugin/NotAHostBuiltin/x#fragment'])
      assert.throws(() => remotePluginAPIPath('NotAHostBuiltin', 'Fixture.clone', path, 'GET'));
  } finally { globalThis.fetch = previous; }
});
