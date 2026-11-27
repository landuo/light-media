import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { compileScript, parse } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

const component = new URL('../src/components/NetworkTest.vue', import.meta.url);
const { descriptor } = parse(readFileSync(component, 'utf8'));
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'nettest' }).content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
const targets = ['tmdb', 'douban', 'github'].map((id) => ({ id, name: id, icon: id }));
const tick = () => new Promise((resolve) => setImmediate(resolve));

function mount(t) {
  const calls = [];
  const hooks = {};
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === 'vue')
      return {
        ...vue,
        onMounted: (fn) => (hooks.open = fn),
        onBeforeUnmount: (fn) => (hooks.close = fn),
      };
    if (name === '../api')
      return {
        api: (path, _body, _method, signal) =>
          new Promise((resolve, reject) => calls.push({ path, signal, resolve, reject })),
        errorText: (error) => error.message,
      };
    if (name.startsWith('vuetify/') || name.startsWith('../assets/')) return {};
    throw new Error(name);
  }, module.exports);
  const scope = vue.effectScope();
  const state = scope.run(() => module.exports.default.setup({}, { expose() {} }));
  const close = () => {
    hooks.close();
    scope.stop();
  };
  t.after(close);
  void hooks.open();
  assert.equal(calls[0].path, '/system/nettest/targets');
  const tested = () =>
    calls.slice(1).map(({ path }) => new URL(path, 'http://test').searchParams.get('target_id'));
  return { calls, state, tested, close };
}

test('auto-tests sequentially, prioritizes one queued retest and continues after failure', async (t) => {
  const { calls, state, tested } = mount(t);
  calls[0].resolve({ targets });
  await tick();
  const [tmdb, douban, github] = state.rows.value;
  assert.deepEqual(tested(), ['tmdb']);
  assert.equal(tmdb.status, 'testing');
  state.retest(tmdb);
  state.retest(github);
  state.retest(github);
  assert.deepEqual(tested(), ['tmdb']);
  calls[1].resolve({ success: true, message: '正常', time_ms: 0 });
  await tick();
  assert.deepEqual(tested(), ['tmdb', 'github']);
  assert.equal(tmdb.time, 0);
  assert.equal(tmdb.status, 'success');
  calls[2].resolve({ success: false, message: 'HTTP 503', time_ms: 45 });
  await tick();
  assert.deepEqual(tested(), ['tmdb', 'github', 'douban']);
  assert.equal(github.status, 'failed');
  assert.equal(github.message, 'HTTP 503');
  assert.equal(github.time, 45);
  calls[3].reject(new Error('连接超时'));
  await tick();
  assert.equal(douban.status, 'failed');
  assert.equal(douban.message, '连接超时');
  state.retest(github);
  assert.equal(github.time, undefined);
  calls[4].resolve({ success: true, message: '正常', time_ms: 21 });
  await tick();
  assert.deepEqual(tested(), ['tmdb', 'github', 'douban', 'github']);
  assert.equal(github.status, 'success');
  assert.equal(github.time, 21);
  assert.equal(douban.status, 'failed');
});

test('closing during discovery aborts it and ignores a late target list', async (t) => {
  const { calls, state, close } = mount(t);
  close();
  assert.equal(calls[0].signal.aborted, true);
  calls[0].resolve({ targets });
  await tick();
  assert.equal(calls.length, 1);
  assert.deepEqual(state.rows.value, []);
});

test('closing mid-test aborts the request and leaves later targets unstarted on late response', async (t) => {
  const { calls, state, tested, close } = mount(t);
  calls[0].resolve({ targets });
  await tick();
  close();
  assert.equal(calls[1].signal.aborted, true);
  calls[1].resolve({ success: true, message: '旧结果', time_ms: 120 });
  await tick();
  assert.deepEqual(tested(), ['tmdb']);
  assert.deepEqual(
    state.rows.value.map((row) => row.status),
    ['testing', 'waiting', 'waiting'],
  );
});
