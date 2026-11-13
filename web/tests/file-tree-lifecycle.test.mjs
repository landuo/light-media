import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as browser from '../src/features/file-browser.ts';
const dir = (path) => ({ type: 'dir', path, name: path.split('/').at(-1) });
async function flush() {
  for (let i = 0; i < 12; i++) {
    await Promise.resolve();
    await vue.nextTick();
  }
}
function mount(t, file, props, respond) {
  const { descriptor } = parse(
    fs.readFileSync(new URL('../src/components/' + file, import.meta.url), 'utf8'),
  );
  const code = ts.transpileModule(compileScript(descriptor, { id: file }).content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const module = { exports: {} },
    scope = vue.effectScope(),
    requests = [];
  let unmount;
  new Function('require', 'exports', 'localStorage', code)(
    (name) => {
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
          api: (url, body, method, signal) => {
            const row = { url, path: new URLSearchParams(url.split('?')[1]).get('path'), signal };
            requests.push(row);
            return respond(row, requests.length);
          },
        };
      if (name === '../features/file-browser') return browser;
      return {};
    },
    module.exports,
    {
      getItem() {
        return null;
      },
      setItem() {},
    },
  );
  const app = scope.run(() => module.exports.default.setup(props, { expose() {}, emit() {} }));
  t.after(() => {
    unmount();
    scope.stop();
  });
  return { app, requests, unmount };
}
function tree(t, respond) {
  return mount(
    t,
    'FileDirectoryTree.vue',
    vue.reactive({
      roots: [{ id: 'data', path: '/data' }],
      currentPath: '/data/a/one',
      refreshKey: 0,
    }),
    respond,
  );
}
test('actual directory tree reuses cached ancestors on ordinary navigation', async (t) => {
  const props = vue.reactive({
    roots: [{ id: 'data', path: '/data' }],
    currentPath: '/data/a/one',
    refreshKey: 0,
  });
  const h = mount(t, 'FileDirectoryTree.vue', props, async (r) => ({
    items: r.path === '/data' ? [dir('/data/a'), dir('/data/b')] : [],
  }));
  await flush();
  assert.deepEqual(
    h.requests.map((r) => r.path),
    ['/data', '/data/a'],
  );
  props.currentPath = '/data/a/two';
  await flush();
  assert.equal(h.requests.length, 2);
  props.currentPath = '/data/b/leaf';
  await flush();
  assert.deepEqual(
    h.requests.map((r) => r.path),
    ['/data', '/data/a', '/data/b'],
  );
});
test('explicit tree refresh only invalidates current and direct parent', async (t) => {
  const props = vue.reactive({
    roots: [{ id: 'data', path: '/data' }],
    currentPath: '/data/a/one',
    refreshKey: 0,
  });
  const h = mount(t, 'FileDirectoryTree.vue', props, async (r) => ({
    items: r.path === '/data' ? [dir('/data/a')] : [],
  }));
  await flush();
  props.currentPath = '/data/a';
  await flush();
  const before = h.requests.length;
  props.refreshKey++;
  await flush();
  assert.deepEqual(
    h.requests.slice(before).map((r) => r.path),
    ['/data', '/data/a'],
  );
});
test('tree navigation cancels old reads and does not publish or continue stale ancestors', async (t) => {
  const props = vue.reactive({
    roots: [{ id: 'data', path: '/data' }],
    currentPath: '/data/a/one',
    refreshKey: 0,
  });
  const h = mount(
    t,
    'FileDirectoryTree.vue',
    props,
    (r) => new Promise((resolve) => (r.resolve = resolve)),
  );
  await flush();
  props.currentPath = '/data/b/leaf';
  await flush();
  assert.equal(h.requests[0].signal.aborted, true);
  h.requests[0].resolve({ items: [dir('/stale')] });
  await flush();
  assert.equal(h.app.children.value['/data'], undefined);
  h.requests[1].resolve({ items: [dir('/data/b')] });
  await flush();
  assert.equal(h.requests[2].path, '/data/b');
  h.requests[2].resolve({ items: [] });
  await flush();
  assert.equal(
    h.requests.some((r) => r.path === '/data/a'),
    false,
  );
  props.refreshKey++;
  await flush();
  const pending = h.requests.at(-1);
  h.unmount();
  assert.equal(pending.signal.aborted, true);
  pending.resolve({ items: [dir('/late')] });
  await flush();
  assert.equal(
    h.app.children.value['/data']?.some((r) => r.path === '/late'),
    false,
  );
});
test('targeted tree refresh drops deleted cached subtrees', async (t) => {
  let deleted = false;
  const props = vue.reactive({
    roots: [{ id: 'data', path: '/data' }],
    currentPath: '/data/a/one',
    refreshKey: 0,
  });
  const h = mount(t, 'FileDirectoryTree.vue', props, async (r) => ({
    items: r.path === '/data' && !deleted ? [dir('/data/a')] : [],
  }));
  await flush();
  assert.ok(h.app.children.value['/data/a']);
  props.currentPath = '/data';
  await flush();
  deleted = true;
  props.refreshKey++;
  await flush();
  assert.equal(h.app.children.value['/data/a'], undefined);
});
test('actual FileManager cancels listings and distinguishes navigation from explicit refresh', async (t) => {
  const h = mount(t, 'FileManager.vue', {}, (r) => new Promise((resolve) => (r.resolve = resolve)));
  h.app.roots.value = [{ id: 'data', path: '/data' }];
  const first = h.app.load('/data/a'),
    second = h.app.load('/data/b');
  assert.equal(h.requests[0].signal.aborted, true);
  h.requests[0].resolve({ items: [dir('/old')] });
  await first;
  assert.equal(h.app.loading.value, true);
  h.requests[1].resolve({ items: [dir('/new')] });
  await second;
  assert.equal(h.app.listing.value[0].path, '/new');
  assert.equal(h.app.refreshKey.value, 0);
  const refresh = h.app.load();
  h.requests[2].resolve({ items: [] });
  await refresh;
  assert.equal(h.app.refreshKey.value, 1);
  const late = h.app.load('/data/c');
  h.unmount();
  assert.equal(h.requests[3].signal.aborted, true);
  h.requests[3].resolve({ items: [dir('/late')] });
  await late;
  assert.deepEqual(h.app.listing.value, []);
});
