import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as dashboard from '../src/features/plugin-dashboard.ts';

function mount(t, api) {
  const { descriptor } = parse(
    readFileSync(new URL('../src/components/PluginDashboard.vue', import.meta.url), 'utf8'),
  );
  const compiled = ts.transpileModule(
    compileScript(descriptor, { id: 'dashboard-loading' }).content,
    {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    },
  ).outputText;
  const hooks = {},
    module = { exports: {} };
  const grid = { on() {}, batchUpdate() {}, engine: { nodes: [] }, setStatic() {}, destroy() {} };
  new Function('require', 'exports', 'window', 'localStorage', 'ResizeObserver', compiled)(
    (name) => {
      if (name === 'vue')
        return {
          ...vue,
          ...Object.fromEntries(
            ['onMounted', 'onUnmounted', 'onActivated', 'onDeactivated'].map((h) => [
              h,
              (fn) => (hooks[h] = fn),
            ]),
          ),
        };
      if (name === 'gridstack') return { GridStack: { init: () => grid } };
      if (name.endsWith('.css')) return {};
      if (name.endsWith('/plugin-dashboard')) return dashboard;
      if (name.endsWith('/api')) return { api, errorText: (e) => e.message };
      if (name === 'vuetify') return { useDisplay: () => ({ mdAndUp: vue.ref(true) }) };
      if (name.startsWith('vuetify/')) return new Proxy({}, { get: (_, key) => ({ name: key }) });
      if (name.endsWith('.vue')) return { default: {} };
      throw Error(name);
    },
    module.exports,
    { innerWidth: 1280, addEventListener() {}, removeEventListener() {} },
    { getItem: () => null, setItem() {} },
    class {
      disconnect() {}
    },
  );
  const scope = vue.effectScope();
  const state = scope.run(() => module.exports.default.setup({}, { expose() {} }));
  t.after(() => {
    hooks.onUnmounted();
    scope.stop();
  });
  return { state, hooks };
}
const meta = [
  { id: 'visible', key: '', name: 'Visible' },
  { id: 'hidden', key: '', name: 'Hidden' },
];
const tick = async () => {
  await new Promise((resolve) => setImmediate(resolve));
  await vue.nextTick();
};

test('dashboard launches independent reads together, loads only enabled data, and discovers hidden settings', async (t) => {
  const requests = [],
    pending = new Map();
  const { state, hooks } = mount(t, (path) => {
    requests.push(path);
    return new Promise((resolve) => pending.set(path, resolve));
  });
  const mounting = hooks.onMounted();
  assert.deepEqual(requests, [
    '/plugins/dashboard/settings/enabled',
    '/plugins/dashboard/settings/order',
    '/plugins/dashboard/settings/desktop',
    '/plugins/dashboard/meta',
  ]);
  pending.get(requests[0])({ value: { visible: true, hidden: false } });
  pending.get(requests[1])({ value: [{ id: 'hidden', key: '' }] });
  pending.get(requests[2])({ value: { items: {} } });
  pending.get(requests[3])(meta);
  await tick();
  assert.ok(requests.includes('/plugins/visible/dashboard?key='));
  assert.ok(!requests.includes('/plugins/hidden/dashboard?key='));
  pending.get('/plugins/visible/dashboard?key=')({ cols: { md: 6 }, elements: [] });
  await mounting;
  assert.equal(state.loading.value, false);
  state.openSettings();
  assert.deepEqual(
    state.settingItems.value.map((x) => x.id),
    ['hidden', 'visible'],
  );
  assert.equal(state.visible.value.length, 1);
  hooks.onDeactivated();
  await state.loadItem(meta[0]);
  assert.equal(requests.filter((x) => x.includes('/visible/dashboard')).length, 1);
  hooks.onActivated();
  assert.equal(requests.filter((x) => x.includes('/visible/dashboard')).length, 2);
  pending.get('/plugins/visible/dashboard?key=')({ cols: { md: 6 } });
  await tick();
  state.layout.value.enabled.hidden = true;
  const loadingHidden = state.loadItem(meta[1]);
  pending.get('/plugins/hidden/dashboard?key=')({ cols: { md: 4 } });
  await loadingHidden;
  assert.equal(state.visible.value.length, 2);
});

test('late dashboard responses cannot restore removed or unmounted widgets', async (t) => {
  let resolve;
  const { state, hooks } = mount(t, () => new Promise((done) => (resolve = done)));
  state.layout.value.enabled = { visible: true };
  state.discoverMetadata(meta);
  const loading = state.loadItem(meta[0]);
  state.discoverMetadata([]);
  resolve({ cols: { md: 6 } });
  await loading;
  assert.equal(state.items.value.length, 0);
  state.discoverMetadata(meta);
  const late = state.loadItem(meta[0]);
  hooks.onUnmounted();
  resolve({ cols: { md: 6 } });
  await late;
  assert.equal(state.visible.value.length, 0);
});
