import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
const { descriptor } = parse(
  fs.readFileSync(new URL('../src/components/Operations.vue', import.meta.url), 'utf8'),
);
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'logs' }).content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
function mount(t) {
  const requests = [];
  let unmount;
  const module = { exports: {} },
    scope = vue.effectScope();
  new Function('require', 'exports', compiled)((name) => {
    if (name === 'vue')
      return {
        ...vue,
        onMounted() {},
        onUnmounted(fn) {
          unmount = fn;
        },
      };
    if (name === '../api')
      return {
        ui: { settings: {} },
        errorText: String,
        api: (path, body, method, signal) =>
          new Promise((resolve, reject) => requests.push({ path, signal, resolve, reject })),
      };
    return {};
  }, module.exports);
  const app = scope.run(() =>
    module.exports.default.setup({ view: 'logs' }, { expose() {}, emit() {} }),
  );
  t.after(() => {
    unmount();
    scope.stop();
  });
  return { app, requests, unmount };
}
async function initial(h) {
  const p = h.app.load();
  h.requests[0].resolve({ entries: [{ id: 'old' }], next_cursor: 'old-cursor' });
  await p;
}
test('actual logs SFC keeps latest query data and ignores old failures/loading', async (t) => {
  const { app, requests } = mount(t);
  const first = app.load();
  app.logQuery.value = 'second';
  const second = app.load();
  assert.equal(requests[0].signal.aborted, true);
  requests[0].resolve({ entries: ['old'] });
  await first;
  assert.equal(app.loading.value, true);
  requests[1].resolve({ entries: ['latest'] });
  await second;
  assert.deepEqual(app.data.value.entries, ['latest']);
  const third = app.load(),
    fourth = app.load();
  requests[2].reject(new Error('stale'));
  await third;
  assert.equal(app.error.value, '');
  assert.equal(app.loading.value, true);
  requests[3].reject(new Error('current'));
  await fourth;
  assert.match(app.error.value, /current/);
});
test('actual logs SFC aborts initial reads on unmount', async (t) => {
  const { app, requests, unmount } = mount(t);
  const p = app.load();
  unmount();
  assert.equal(requests[0].signal.aborted, true);
  requests[0].resolve({ entries: ['late'] });
  await p;
  assert.notDeepEqual(app.data.value?.entries, ['late']);
});
test('old pagination cannot append or clear loading during a replacement query', async (t) => {
  const h = mount(t);
  await initial(h);
  const { app, requests } = h;
  const more = app.moreLogs();
  app.logQuery.value = 'new';
  const changed = app.load();
  assert.equal(requests[1].signal.aborted, true);
  requests[1].resolve({ entries: [{ id: 'stale' }], next_cursor: 'stale', gap: true });
  await more;
  assert.equal(app.loading.value, true);
  requests[2].resolve({ entries: [{ id: 'new' }], next_cursor: 'new' });
  await changed;
  const next = app.moreLogs();
  requests[3].resolve({ entries: [{ id: 'new' }, { id: 'page' }], next_cursor: 'next' });
  await next;
  assert.deepEqual(app.data.value.entries, [{ id: 'new' }, { id: 'page' }]);
  assert.equal(app.data.value.next_cursor, 'next');
  assert.equal(app.logGap.value, '');
});
test('pagination cancels on unmount without changing the snapshot', async (t) => {
  const h = mount(t);
  await initial(h);
  const p = h.app.moreLogs();
  h.unmount();
  assert.equal(h.requests[1].signal.aborted, true);
  h.requests[1].resolve({ entries: [{ id: 'late' }], next_cursor: 'late' });
  await p;
  assert.deepEqual(h.app.data.value.entries, [{ id: 'old' }]);
  assert.equal(h.app.data.value.next_cursor, 'old-cursor');
});
test('draft filter changes reject pending pagination and never reuse old cursor', async (t) => {
  const h = mount(t);
  await initial(h);
  const p = h.app.moreLogs();
  h.app.logQuery.value = 'draft';
  h.requests[1].resolve({ entries: [{ id: 'obsolete' }] });
  await p;
  assert.deepEqual(h.app.data.value.entries, [{ id: 'old' }]);
  const refresh = h.app.moreLogs();
  assert.match(h.requests[2].path, /q=draft/);
  assert.doesNotMatch(h.requests[2].path, /cursor=/);
  h.requests[2].resolve({ entries: [{ id: 'draft' }] });
  await refresh;
  assert.deepEqual(h.app.data.value.entries, [{ id: 'draft' }]);
});
test('old page returning after new query completion cannot corrupt rows cursor or gap', async (t) => {
  const h = mount(t);
  await initial(h);
  const p = h.app.moreLogs();
  h.app.logQuery.value = 'new';
  const changed = h.app.load();
  h.requests[2].resolve({ entries: [{ id: 'new' }], next_cursor: 'new' });
  await changed;
  h.requests[1].resolve({ entries: [{ id: 'old-page' }], next_cursor: 'stale', gap: true });
  await p;
  assert.deepEqual(h.app.data.value.entries, [{ id: 'new' }]);
  assert.equal(h.app.data.value.next_cursor, 'new');
  assert.equal(h.app.logGap.value, '');
  assert.equal(h.app.loading.value, false);
});
