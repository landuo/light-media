import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as logging from '../src/features/plugin-logs.ts';
const source = readFileSync(
  new URL('../src/components/PluginLogDialog.vue', import.meta.url),
  'utf8',
);
const { descriptor } = parse(source);
const compiled = ts.transpileModule(
  compileScript(descriptor, { id: 'PluginLogDialog', inlineTemplate: true }).content,
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText;
const record = (id, message) => ({
  id,
  plugin_instance_id: 'OwnedLog',
  level: 'INFO',
  time: '2026-10-26T12:00:00.001Z',
  module: 'plugins',
  message,
});
async function mount(t, { history = [record('initial', 'first')], fail = false } = {}) {
  const fixture = {
    history,
    fail,
    requests: [],
    streams: [],
    timers: new Map(),
    listeners: new Map(),
  };
  let sequence = 0;
  const mounted = [],
    unmounted = [],
    module = { exports: {} };
  class EventSource {
    constructor(url) {
      this.url = url;
      this.listeners = new Map();
      fixture.streams.push(this);
    }
    close() {
      this.closed = true;
    }
    addEventListener(name, fn) {
      this.listeners.set(name, fn);
    }
  }
  const document = {
    hidden: false,
    addEventListener: (n, f) => fixture.listeners.set(n, f),
    removeEventListener: (n) => fixture.listeners.delete(n),
  };
  const setTimeout = (fn, delay) => {
      const id = ++sequence;
      fixture.timers.set(id, { fn, delay });
      return id;
    },
    clearTimeout = (id) => fixture.timers.delete(id);
  new Function(
    'require',
    'exports',
    'EventSource',
    'document',
    'setTimeout',
    'clearTimeout',
    compiled,
  )(
    (name) => {
      if (name === 'vue')
        return {
          ...vue,
          onMounted: (fn) => mounted.push(fn),
          onUnmounted: (fn) => unmounted.push(fn),
        };
      if (name === 'vuetify') return { useDisplay: () => ({ mdAndUp: vue.ref(true) }) };
      if (name.startsWith('vuetify/')) return new Proxy({}, { get: (_, key) => ({ name: key }) });
      if (name.endsWith('.vue')) return { name: 'PluginDialogCloseBtn' };
      if (name === '../features/plugin-logs') return logging;
      if (name === '../api')
        return {
          api: async (path) => {
            fixture.requests.push(path);
            if (fixture.fail) throw Error('owned history failure');
            return { history: { entries: fixture.history }, resume_id: 'boot:17' };
          },
          apiURL: (path) => '/api' + path,
          errorText: (e) => e.message,
        };
      throw Error('unexpected module ' + name);
    },
    module.exports,
    EventSource,
    document,
    setTimeout,
    clearTimeout,
  );
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup(
      { plugin: { instance_id: 'OwnedLog' } },
      { expose() {}, emit() {} },
    ),
  );
  const flush = async () => {
    for (let i = 0; i < 4; i++) await vue.nextTick();
  };
  for (const fn of mounted) await fn();
  await flush();
  const nodes = () => {
    const out = [];
    const visit = (n) => {
      if (!n) return;
      if (Array.isArray(n)) {
        n.forEach(visit);
        return;
      }
      if (typeof n !== 'object') return;
      out.push(n);
      if (n.children?.default) visit(n.children.default());
      else visit(n.children);
    };
    visit(render({}, []));
    return out;
  };
  const run = async (delay) => {
    const match = [...fixture.timers].find(([, v]) => v.delay === delay);
    assert.ok(match, 'timer ' + delay);
    fixture.timers.delete(match[0]);
    await match[1].fn();
    await flush();
  };
  const stop = () => {
    unmounted.forEach((fn) => fn());
    scope.stop();
  };
  t.after(stop);
  return {
    fixture,
    document,
    flush,
    nodes,
    run,
    stop,
    lines: () => nodes().filter((n) => n.props?.class === 'logging-record-line'),
    text: () =>
      JSON.stringify(
        nodes()
          .filter((n) => typeof n.children === 'string')
          .map((n) => n.children),
      ),
  };
}

test('actual log dialog starts with 50 physical lines then receives scoped, buffered source lines', async (t) => {
  const app = await mount(t, {
    history: [record('initial', Array.from({ length: 70 }, (_, i) => 'owned ' + i).join('\n'))],
  });
  assert.equal(app.lines().length, 50);
  assert.doesNotMatch(app.text(), /owned 19/);
  assert.match(app.text(), /owned 69/);
  await app.run(300);
  const stream = app.fixture.streams[0];
  assert.match(stream.url, /last_event_id=boot%3A17/);
  stream.onopen();
  stream.onmessage({
    data: JSON.stringify(record('live', 'next\ncontinuation')),
    lastEventId: 'boot:18',
  });
  stream.onmessage({
    data: JSON.stringify({
      ...record('sibling', 'must not appear'),
      plugin_instance_id: 'Sibling',
    }),
    lastEventId: 'boot:19',
  });
  assert.equal(app.lines().length, 50);
  await app.run(80);
  assert.equal(app.lines().length, 52);
  assert.match(app.text(), /continuation/);
  assert.doesNotMatch(app.text(), /must not appear/);
  const level = app.nodes().find((n) => n.type?.name === 'VSelect');
  level.props['onUpdate:modelValue']('INFO');
  await app.flush();
  assert.equal(app.lines().length, 1, 'continuation does not inherit INFO');
  const query = app.nodes().find((n) => n.type?.name === 'VTextField');
  query.props['onUpdate:modelValue']('  NEXT ');
  await app.flush();
  assert.equal(app.lines().length, 1);
  app.stop();
  assert.ok(stream.closed);
  assert.equal(app.fixture.timers.size, 0);
  assert.equal(app.fixture.listeners.size, 0);
});

test('actual log dialog pause/resume and gap use the existing host stream boundary', async (t) => {
  const app = await mount(t);
  await app.run(300);
  const first = app.fixture.streams[0];
  first.onmessage({ data: JSON.stringify(record('live', 'live')), lastEventId: 'boot:18' });
  app
    .nodes()
    .find((n) => n.props?.['aria-label'] === '暂停日志流')
    .props.onClick();
  await app.flush();
  assert.ok(first.closed);
  assert.equal(app.lines().length, 2);
  app
    .nodes()
    .find((n) => n.props?.['aria-label'] === '恢复日志流')
    .props.onClick();
  await app.flush();
  const second = app.fixture.streams[1];
  assert.match(second.url, /boot%3A18/);
  second.listeners.get('gap')();
  await app.flush();
  assert.ok(second.closed);
  assert.equal(app.fixture.requests.length, 2);
  assert.equal(app.lines().length, 1);
});

test('actual log dialog shows history failure and three reconnect attempts then explicit retry', async (t) => {
  const app = await mount(t, { fail: true });
  assert.match(app.text(), /owned history failure/);
  assert.equal(app.fixture.streams.length, 0);
  app.fixture.fail = false;
  const retry = app
    .nodes()
    .find(
      (n) =>
        n.type?.name === 'VBtn' &&
        n.children?.default?.().some((c) => c.children?.trim?.() === '重试'),
    );
  await retry.props.onClick();
  await app.flush();
  await app.run(300);
  for (let i = 0; i < 3; i++) {
    app.fixture.streams.at(-1).onerror();
    await app.flush();
    await app.run(3000);
  }
  app.fixture.streams.at(-1).onerror();
  await app.flush();
  assert.match(app.text(), /请点击重试/);
  assert.equal(
    [...app.fixture.timers.values()].some((v) => v.delay === 3000),
    false,
  );
});
