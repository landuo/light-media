import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as dashboard from '../src/features/plugin-dashboard.ts';

// Fixed dashboard.vue:118,439,1124 keeps enableConfig separate from each
// device geometry. Reproduce the real browser defect in the actual target SFC,
// with only DOM/lifecycle/GridStack/API boundaries replaced by owned inputs.
function mountDashboard(t) {
  const { descriptor } = parse(
    readFileSync(new URL('../src/components/PluginDashboard.vue', import.meta.url), 'utf8'),
  );
  const compiled = compileScript(descriptor, { id: 'profile-regression' });
  const code = ts.transpileModule(compiled.content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const module = { exports: {} },
    window = { innerWidth: 1280 };
  let resolveGeometry;
  const remote = new Promise((resolve) => {
    resolveGeometry = resolve;
  });
  const scope = vue.effectScope();
  const require = (name) => {
    if (name === 'vue')
      return { ...vue, onMounted() {}, onUnmounted() {}, onActivated() {}, onDeactivated() {} };
    if (name === 'gridstack') return { GridStack: {} };
    if (name.endsWith('.css')) return {};
    if (name.endsWith('/plugin-dashboard')) return dashboard;
    if (name.endsWith('/api')) return { api: () => remote, errorText: (e) => e.message };
    if (name === 'vuetify') return { useDisplay: () => ({ mdAndUp: vue.ref(true) }) };
    if (name.startsWith('vuetify/')) return new Proxy({}, { get: (_, key) => ({ name: key }) });
    if (name.endsWith('.vue')) return { default: {} };
    throw Error('Unexpected import ' + name);
  };
  const storage = { getItem: () => JSON.stringify({ items: {}, enabled: { source: false } }) };
  new Function('require', 'exports', 'window', 'localStorage', code)(
    require,
    module.exports,
    window,
    storage,
  );
  const state = scope.run(() => module.exports.default.setup({}, { expose() {} }));
  t.after(() => scope.stop());
  return { state, window, resolveGeometry };
}

test('device profile changes preserve shared visible widgets before and after remote geometry loads', async (t) => {
  const { state, window, resolveGeometry } = mountDashboard(t);
  state.layout.value = {
    items: { source: { x: 0, y: 0, w: 8, h: 22 } },
    enabled: { 'source:summary': true, 'clone:details': true },
  };
  state.items.value = [
    { id: 'source', key: 'summary', cols: { md: 8 } },
    { id: 'clone', key: 'details', cols: { md: 8 } },
  ];
  assert.equal(state.visible.value.length, 2);
  window.innerWidth = 900;
  const changing = state.switchProfile();
  assert.equal(state.profile.value, 'tablet');
  assert.equal(
    state.visible.value.length,
    2,
    'shared enabled choices must survive immediate cached geometry',
  );
  resolveGeometry({ value: { items: {} } });
  await changing;
  assert.equal(
    state.visible.value.length,
    2,
    'empty remote geometry must not hide selected widgets',
  );
  assert.deepEqual(
    { ...state.layout.value.enabled },
    { 'source:summary': true, 'clone:details': true },
  );
});

// Fixed dashboard.vue:1056 passes the current column to GridStack.save.
// Use GridStack12.6.0's actual geometry engine, including its widest-layout
// cache, to distinguish visible tablet geometry from cached desktop geometry.
test('capturing a device layout saves its current geometry rather than the widest cached layout', (t) => {
  const transpile = (code) =>
    ts.transpileModule(code, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText;
  const utils = { exports: {} },
    engineModule = { exports: {} };
  new Function(
    'exports',
    transpile(
      readFileSync(new URL('../node_modules/gridstack/dist/utils.js', import.meta.url), 'utf8'),
    ),
  )(utils.exports);
  new Function(
    'require',
    'exports',
    transpile(
      readFileSync(
        new URL('../node_modules/gridstack/dist/gridstack-engine.js', import.meta.url),
        'utf8',
      ),
    ),
  )((name) => {
    assert.equal(name, './utils');
    return utils.exports;
  }, engineModule.exports);
  const engine = new engineModule.exports.GridStackEngine({
    column: 6,
    nodes: [
      { id: 'source:summary', _id: 1, x: 0, y: 0, w: 4, h: 25 },
      { id: 'clone:details', _id: 2, x: 0, y: 25, w: 6, h: 20 },
    ],
  });
  engine.cacheLayout(
    [
      { id: 'source:summary', _id: 1, x: 0, y: 0, w: 8, h: 22 },
      { id: 'clone:details', _id: 2, x: 0, y: 22, w: 6, h: 22 },
    ],
    12,
  );
  const { state } = mountDashboard(t);
  state.profile.value = 'tablet';
  state.layout.value = { items: {}, enabled: { 'source:summary': true, 'clone:details': true } };
  // GridStack.save delegates to engine.save; replace only its DOM wrapper.
  state.grid = {
    getColumn: () => engine.column,
    save: (content, options, callback, column) => engine.save(content, callback, column),
  };
  state.capture('source:summary');
  assert.deepEqual({ ...state.layout.value.items['source:summary'] }, { x: 0, y: 0, w: 4, h: 25 });
  assert.deepEqual({ ...state.layout.value.items['clone:details'] }, { x: 0, y: 25, w: 6 });
});
