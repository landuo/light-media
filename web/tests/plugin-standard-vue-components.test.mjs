import './helpers/plugin-axios-fetch-adapter.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as Vue from 'vue';
import * as realAPI from '../src/api.ts';
import * as forms from '../src/features/plugin-forms.ts';
import * as navigation from '../src/features/plugin-navigation.ts';
import * as remoteAPI from '../src/features/plugin-remote-api.ts';
import { createPluginRemoteLoader, loadPluginAppRemote } from '../src/features/plugin-federation.ts';

const names = ['PluginConfigDialog', 'PluginDataDialog', 'PluginDashboardWidget', 'PluginAppPage'];
const plugin = { plugin_id: 'Standard.clone', instance_id: 'Standard.clone', installed: true, available: true, name: 'Fixture', version: '2.0' };
const digest = 'c'.repeat(64);
const meta = { instance_id: plugin.instance_id, plugin_id: 'StandardSource', version: '2.0',
  package_sha256: digest, contract: 'light-media.vue-federation/v1',
  exposes: ['Config', 'Page', 'Dashboard', 'AppPage'], shared: ['vue'], api_contract: 'light-media.plugin-api/v1',
  entry_path: '/plugins/Standard.clone/frontend/' + digest + '/assets/remoteEntry.js' };
const propsFor = (name) => name === 'PluginAppPage' ? { route: 'plugin-app/Standard.clone/archive' }
  : name === 'PluginDashboardWidget' ? { item: { id: plugin.instance_id, key: 'stats', render_mode: 'vue', name: 'Fixture' } }
  : { plugin: { ...plugin } };
const deferred = () => { let resolve; const promise = new Promise((r) => resolve = r); return { promise, resolve }; };
function fixture(name, { descriptor = meta, navKey = 'archive', getEntry, getForm, props = propsFor(name) } = {}) {
  const events = [], calls = [], mounted = [], unmounted = [], imported = [], started = [];
  const fetcher = async (url, options = {}) => {
    calls.push({ url, options });
    let data;
    if (url === '/api/plugins') data = [{ ...plugin }];
    else if (url === '/api/plugins/navigation') data = [{ plugin_id: plugin.instance_id, instance_id: plugin.instance_id, nav_key: navKey }];
    else if (url.endsWith('/frontend')) data = descriptor;
    else if (url.endsWith('/form')) data = getForm ? await getForm() : { render_mode: 'vue', conf: [], model: { draft: 'retained' } };
    else if (url.endsWith('/page')) data = { render_mode: 'vue' };
    else if (url.endsWith('/status')) return new Response(JSON.stringify({ success: true, data: { bound: true } }));
    else throw new Error('unexpected request: ' + url);
    return new Response(JSON.stringify({ data }));
  };
  const load = createPluginRemoteLoader({ fetcher, importEntry: async (url) => {
    imported.push(url);
    if (getEntry) return getEntry();
    return { init() {}, async get(key) { return () => ({ name: key, setup() {} }); } };
  } });
  const source = readFileSync(new URL('../src/components/' + name + '.vue', import.meta.url), 'utf8');
  const { descriptor: sfc } = parse(source);
  const code = ts.transpileModule(compileScript(sfc, { id: name }).content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const module = { exports: {} };
  new Function('require', 'exports', code)((path) => {
    if (path === 'vue') return { ...Vue, useId: () => 'fixture', onMounted: (callback) => mounted.push(callback), onUnmounted: (callback) => unmounted.push(callback), watch(source,callback,options){return Vue.watch(source,(...args)=>{const pending=callback(...args);if(pending?.then)started.push(pending);return pending},options)} };
    if (path === 'vuetify') return { useDisplay: () => ({ mdAndUp: Vue.ref(true) }) };
    if (path === '../api') return realAPI;
    if (path === '../features/plugin-forms') return forms;
    if (path === '../features/plugin-navigation') return navigation;
    if (path === '../features/plugin-remote-api') return remoteAPI;
    if (path === '../features/plugin-federation') return { loadPluginRemote: load, loadPluginAppRemote: (instance, key, options) => loadPluginAppRemote(instance, key, options, load) };
    if (path.startsWith('vuetify/') || path.endsWith('.vue')) return {};
    throw new Error('unexpected component import: ' + path);
  }, module.exports);
  const scope = Vue.effectScope();
  const state = scope.run(() => module.exports.default.setup(Vue.reactive(props), { expose() {}, emit: (...args) => events.push(args) }));
  const previous = globalThis.fetch; globalThis.fetch = fetcher;
  let stopped = false;
  function stop() { if (stopped) return; stopped = true; for (const callback of unmounted) callback(); scope.stop(); globalThis.fetch = previous; }
  return { state, calls, events, imported, stop, unmount: () => unmounted.forEach((callback) => callback()), mount: async () => {await Promise.all(mounted.map((callback) => callback()));await Vue.nextTick();await Promise.all(started);await new Promise(setImmediate)}, sfc };
}

for (const name of names) {
  test('actual ' + name + ' SFC uses standard metadata, not a host builtin or clone as source API ID', async (t) => {
    const f = fixture(name); t.after(f.stop); await f.mount();
    assert.equal(f.state.error.value, '');
    assert.ok(f.state.remote.value);
    assert.equal(f.imported[0], '/api' + meta.entry_path);
    await f.state.remoteAPI.value.get('plugin/Standard.clone/status');
    const apiCall = f.calls.at(-1);
    assert.equal(apiCall.url, '/api/v1/plugin/Standard.clone/status');
    assert.equal(apiCall.options.credentials, 'same-origin');
    assert.equal(apiCall.options.headers.get('Authorization'), undefined);
    if (name === 'PluginAppPage') {
      assert.equal(f.state.navKey.value, 'archive');
      assert.equal(f.state.pluginID.value, 'Standard.clone');
    }
    if (name === 'PluginConfigDialog') assert.equal(f.state.model.value.draft, 'retained');
    assert.deepEqual(compileTemplate({ source: f.sfc.template.content, filename: name + '.vue', id: name }).errors, []);
  });
  test('actual ' + name + ' SFC cancellation aborts loader and never publishes a late remote', async (t) => {
    const pending = deferred();
    const f = fixture(name, { getEntry: () => pending.promise }); t.after(f.stop);
    const running = f.mount(); await new Promise(setImmediate);
    f.unmount();
    pending.resolve({ init() {}, async get() { return () => ({ setup() {} }); } });
    await running;
    assert.equal(f.state.remote.value, undefined);
    assert.equal(f.state.error.value, '');
    // AppPage may already have completed the absent AppPage<Key> candidate;
    // the active candidate is the final descriptor request.
    const metadata = f.calls.filter((item) => item.url.endsWith('/frontend')).at(-1);
    assert.equal(metadata.options.signal.aborted, true);
  });
  test('actual ' + name + ' SFC exposes loader errors instead of a fake success component', async (t) => {
    const f = fixture(name, { descriptor: { ...meta, contract: 'unsupported/v9' } }); t.after(f.stop);
    await f.mount();
    assert.match(f.state.error.value, /契约不受支持/);
    assert.equal(f.state.remote.value, undefined);
    assert.equal(f.imported.length, 0);
  });
}

test('fixed AppPage directly loads a URL even when the sidebar declares a different nav key', async (t) => {
  const f = fixture('PluginAppPage', { navKey: 'main' }); t.after(f.stop); await f.mount();
  assert.equal(f.state.error.value, '');assert.ok(f.state.remote.value);
  assert.equal(f.state.navKey.value,'archive');assert.equal(f.imported.length,1);
  assert.ok(!f.calls.some(x=>x.url==='/api/plugins/navigation'),'sidebar controls menu placement, not AppPage loading');
});
test('actual Config never imports after a cancelled form request; unknown schema fails explicitly', async (t) => {
  const pending = deferred(); const f = fixture('PluginConfigDialog', { getForm: () => pending.promise });
  const running = f.mount(); f.unmount(); pending.resolve({ render_mode: 'vue', model: {} }); await running;
  assert.equal(f.imported.length, 0); f.stop();
  const invalid = fixture('PluginConfigDialog', { getForm: () => ({ render_mode: 'unsupported', model: {} }) }); t.after(invalid.stop);
  await invalid.mount(); assert.match(invalid.state.error.value, /格式无效/); assert.equal(invalid.imported.length, 0);
});
test('actual Config and Page generic successful API mutation emits changed, without special plugin IDs', async () => {
  for (const name of ['PluginConfigDialog', 'PluginDataDialog', 'PluginAppPage']) {
    const f = fixture(name);
    try { await f.mount(); await f.state.remoteAPI.value.post('plugin/StandardSource/status', { enabled: true }); assert.ok(f.events.some(([event]) => event === 'changed')); }
    finally { f.stop(); }
  }
});
