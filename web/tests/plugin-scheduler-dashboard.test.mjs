import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

function mount(t, fetchList, allowRefresh = true) {
  const name = 'PluginSchedulerDashboard';
  const { descriptor } = parse(readFileSync(new URL('../src/components/' + name + '.vue', import.meta.url), 'utf8'));
  const script = compileScript(descriptor, { id: name }).content;
  const js = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const starts = [], stops = [], events = [], timers = new Map(), listeners = new Map(), errors = [];
  const document = { hidden: false, addEventListener: (k, fn) => listeners.set(k, fn), removeEventListener: (k) => listeners.delete(k) };
  let serial = 0;
  const mod = { exports: {} };
  new Function('require', 'exports', 'document', 'setInterval', 'clearInterval', 'console', js)(
    (key) => {
      if (key === 'vue') return { ...vue, onMounted: (fn) => starts.push(fn), onUnmounted: (fn) => stops.push(fn) };
      if (key === '../api') return { api: fetchList };
      if (key.startsWith('vuetify/')) return {};
      throw Error(key);
    },
    mod.exports, document,
    (fn, ms) => { const id = ++serial; timers.set(id, { fn, ms }); return id; },
    (id) => timers.delete(id), { error: (e) => errors.push(e.message) },
  );
  const props = vue.reactive({ allowRefresh });
  const state = mod.exports.default.setup(props, { expose() {}, emit: (...v) => events.push(v) });
  const stop = () => stops.forEach((fn) => fn());
  t.after(stop);
  return { state, props, starts, stop, timers, listeners, document, errors, events, descriptor };
}

test('source scheduler card reads once, polls at60s and retains source order/name/next/status', async (t) => {
  const calls = [], rows = [{ id: 'Owned_running', name: '运行任务', next_run: null, status: '正在运行' }, { id: 'Owned_wait', name: '等待任务', next_run: '2026-10-30 12:00:00', status: '等待' }];
  const f = mount(t, async (path) => { calls.push(path); return rows; });
  await f.starts[0]();
  assert.deepEqual(calls, ['/plugins/services']);
  assert.deepEqual(JSON.parse(JSON.stringify(f.state.schedulerList.value)), rows);
  assert.equal(f.timers.size, 1);
  const timer = [...f.timers.values()][0];
  assert.equal(timer.ms, 60000);
  timer.fn(); await Promise.resolve(); await Promise.resolve();
  assert.equal(calls.length, 2);
  assert.ok(f.descriptor.template.content.includes('{{ item.name[0] }}'));
  assert.ok(f.descriptor.template.content.includes('{{ item.next_run }}'));
  assert.ok(f.descriptor.template.content.includes('{{ item.status }}'));
  assert.ok(!f.descriptor.template.content.includes('执行'));
});

test('source failed refresh retains displayed rows and later refresh recovers without duplicate calls', async (t) => {
  let calls = 0, mode = 'ok', release;
  const f = mount(t, async () => {
    calls++;
    if (mode === 'failed') throw Error('owned schedule failure');
    if (mode === 'held') return new Promise((resolve) => { release = resolve; });
    return [{ id: mode, name: mode, next_run: null, status: '等待' }];
  });
  await f.starts[0]();
  mode = 'failed'; await f.state.loadSchedulerList();
  assert.equal(f.state.schedulerList.value[0].id, 'ok');
  assert.deepEqual(f.errors, ['owned schedule failure']);
  mode = 'held'; const pending = f.state.loadSchedulerList();
  await f.state.loadSchedulerList();
  assert.equal(calls, 3);
  release([{ id: 'recovered', name: 'Recovered', next_run: null, status: '等待' }]);
  await pending;
  assert.equal(f.state.schedulerList.value[0].id, 'recovered');
});

test('allowRefresh gates reads, hidden pause/resume does not read immediately, unmount clears timer/listener', async (t) => {
  let calls = 0;
  const f = mount(t, async () => { calls++; return []; }, false);
  await f.starts[0]();
  assert.equal(calls, 0);
  f.props.allowRefresh = true;
  [...f.timers.values()][0].fn(); await Promise.resolve(); await Promise.resolve();
  assert.equal(calls, 1);
  f.document.hidden = true; f.listeners.get('visibilitychange')();
  assert.equal(f.timers.size, 0);
  f.document.hidden = false; f.listeners.get('visibilitychange')();
  assert.equal(f.timers.size, 1);
  assert.equal(calls, 1);
  f.stop();
  assert.equal(f.timers.size, 0);
  assert.equal(f.listeners.size, 0);
  await f.state.loadSchedulerList();
  assert.equal(calls, 1);
});

test('late pending read cannot restart a disposed card timer', async (t) => {
  let release;
  const f = mount(t, () => new Promise((resolve) => { release = resolve; }));
  const pending = f.starts[0]();
  f.stop();
  release([{ id: 'late', name: 'late', next_run: null, status: '等待' }]);
  await pending;
  assert.equal(f.timers.size, 0);
  assert.equal(f.listeners.size, 0);
  assert.deepEqual(f.state.schedulerList.value, []);
});

test('initial hidden mount preserves source interval anchor when first becoming visible', async (t) => {
  let calls = 0;
  const f = mount(t, async () => { calls++; return []; });
  f.document.hidden = true;
  await f.starts[0]();
  assert.equal(calls, 1, 'fixed useDataRefresh performs initial read even hidden');
  assert.equal(f.timers.size, 1);
  const [[id, timer]] = [...f.timers];
  timer.fn(); await Promise.resolve();
  assert.equal(calls, 1, 'hidden interval does not read');
  f.document.hidden = false; f.listeners.get('visibilitychange')();
  assert.equal([...f.timers.keys()][0], id, 'visibility retains initial60s phase, not a fresh60s delay');
  timer.fn(); await Promise.resolve(); await Promise.resolve();
  assert.equal(calls, 2);
});
