import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

const oracle = JSON.parse(
  readFileSync(
    new URL(
      '../../testdata/standard-module-host/download-dialog-source-oracle.json',
      import.meta.url,
    ),
    'utf8',
  ),
);
const source = readFileSync(
  process.env.ADD_DOWNLOAD_DIALOG_SOURCE ||
    new URL('../src/components/AddDownloadDialog.vue', import.meta.url),
  'utf8',
);
const compiled = ts.transpileModule(
  compileScript(parse(source).descriptor, { id: 'AddDownloadDialog', inlineTemplate: true })
    .content,
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText;

async function mount(
  t,
  {
    states = [
      { id: 'native_owned', state: 'submitting' },
      { id: 'native_owned', state: 'accepted' },
    ],
    networkError = false,
  } = {},
) {
  const fixture = { requests: [], events: [], toasts: [], timers: new Map(), states, networkError };
  const unmount = [];
  let serial = 0;
  const module = { exports: {} };
  new Function('require', 'exports', 'setTimeout', 'clearTimeout', compiled)(
    (name) => {
      if (name === 'vue') return { ...vue, onBeforeUnmount: (fn) => unmount.push(fn) };
      if (name.startsWith('vuetify/')) return new Proxy({}, { get: (_, key) => ({ name: key }) });
      if (name.endsWith('.vue'))
        return { __esModule: true, default: { name: name.split('/').at(-1).replace('.vue', '') } };
      if (name === '../api')
        return {
          ui: { settings: { sites: [{ id: 'owned-site', name: 'Owned site' }] } },
          bytes: () => '',
          errorText: (e) => e.message,
          toast: (...args) => fixture.toasts.push(args),
          api: async (path, body) => {
            fixture.requests.push({ path, body });
            if (body) return { id: 'native_owned', state: 'planned' };
            if (fixture.networkError) throw Error('owned network failure');
            const result = fixture.states.shift();
            assert.ok(result, 'unexpected status query');
            return result;
          },
        };
      throw Error('unexpected component import ' + name);
    },
    module.exports,
    (fn, delay) => {
      const id = ++serial;
      fixture.timers.set(id, { fn, delay });
      return id;
    },
    (id) => fixture.timers.delete(id),
  );
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup(
      {
        candidate: { resource: { site_id: 'owned-site', id: '101', title: 'Owned resource' } },
        request: { query: 'Owned', page: 1 },
      },
      { expose() {}, emit: (...args) => fixture.events.push(args) },
    ),
  );
  const flush = async () => {
    for (let i = 0; i < 6; i++) {
      await Promise.resolve();
      await vue.nextTick();
    }
  };
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
  const submit = () =>
    nodes()
      .find((n) => n.type === 'form')
      .props.onSubmit({ preventDefault() {}, stopPropagation() {} });
  const tick = async () => {
    const found = [...fixture.timers][0];
    assert.ok(found, 'native completion poll');
    assert.equal(found[1].delay, 250);
    fixture.timers.delete(found[0]);
    found[1].fn();
    await flush();
  };
  const stop = () => {
    unmount.splice(0).forEach((fn) => fn());
    scope.stop();
  };
  t.after(stop);
  await flush();
  return { fixture, flush, nodes, submit, tick, stop };
}

test('actual Add dialog waits for real acceptance, prevents repeat submits and only then emits source done', async (t) => {
  const app = await mount(t);
  const pending = app.submit();
  await app.flush();
  assert.deepEqual(app.fixture.events, [], 'planned queue receipt cannot become source success');
  await app.submit();
  assert.equal(app.fixture.requests.filter((r) => r.body).length, 1);
  await app.tick();
  assert.deepEqual(app.fixture.events, []);
  await app.tick();
  await pending;
  assert.deepEqual(app.fixture.events, [['done', { id: 'native_owned', state: 'accepted' }]]);
  assert.ok(oracle.cases.find((c) => c.mode === 'success').marked_downloaded);
});

test('source business failure emits error and toast without done; each existing parent closes on error', async (t) => {
  const sample = oracle.cases.find((c) => c.mode === 'business_failure');
  assert.equal(sample.parent_closed, true);
  assert.equal(sample.marked_downloaded, false);
  const app = await mount(t, {
    states: [{ id: 'native_owned', state: 'failed', error: '未找到下载器' }],
  });
  const pending = app.submit();
  await app.flush();
  await app.tick();
  await pending;
  assert.deepEqual(app.fixture.events, [['error', '未找到下载器']]);
  assert.deepEqual(app.fixture.toasts, [
    ['Owned site Owned resource 添加下载失败：未找到下载器', true],
  ]);
  for (const name of ['Resources', 'KeywordResources', 'SiteResourceDialog']) {
    const template = parse(
      readFileSync(new URL('../src/components/' + name + '.vue', import.meta.url), 'utf8'),
    ).descriptor.template.content;
    assert.match(template, /<AddDownloadDialog[\s\S]*?@error=/);
  }
});

test('status transport failure retains the form/draft and permits an explicit retry', async (t) => {
  assert.equal(oracle.cases.find((c) => c.mode === 'transport_failure').parent_closed, false);
  const app = await mount(t, {
    networkError: true,
    states: [{ id: 'native_owned', state: 'accepted' }],
  });
  let target = app.nodes().find((n) => n.type?.name === 'DownloadTarget');
  target.props['onUpdate:downloader']('owned-downloader');
  target.props['onUpdate:downloadPath']('/owned/downloads');
  const pending = app.submit();
  await app.flush();
  await app.tick();
  await pending;
  assert.deepEqual(app.fixture.events, []);
  assert.ok(
    app.nodes().some((n) => n.props?.role === 'alert' && n.children === 'owned network failure'),
  );
  target = app.nodes().find((n) => n.type?.name === 'DownloadTarget');
  assert.equal(target.props.downloader, 'owned-downloader');
  assert.equal(target.props['download-path'], '/owned/downloads');
  app.fixture.networkError = false;
  const retry = app.submit();
  await app.flush();
  await app.tick();
  await retry;
  assert.equal(app.fixture.requests.filter((r) => r.body).length, 2);
  assert.equal(app.fixture.events[0][0], 'done');
});

test('unmount cancels the native completion wait and suppresses late success', async (t) => {
  const app = await mount(t);
  const pending = app.submit();
  await app.flush();
  app.stop();
  await pending;
  assert.equal(app.fixture.timers.size, 0);
  assert.deepEqual(app.fixture.events, []);
  assert.equal(app.fixture.requests.length, 1);
});
