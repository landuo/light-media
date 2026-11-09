import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as browser from '../src/features/file-browser.ts';

const source = readFileSync(new URL('../src/components/FileManager.vue', import.meta.url), 'utf8');
const { descriptor } = parse(source);
const script = compileScript(descriptor, { id: 'file-selection-test', inlineTemplate: true });
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

async function openFiles(t, { initialPath = '/media/download', container = false } = {}) {
  const fixture = {
    items: [
      { path: '/media/download/one.mkv', name: 'one.mkv', type: 'file' },
      { path: '/media/download/two.mkv', name: 'two.mkv', type: 'file' },
      { path: '/media/download/folder', name: 'folder', type: 'dir' },
    ],
    requests: [],
  };
  const mounted = [];
  const apiModule = {
    api: async (path) => {
      fixture.requests.push(path);
      if (path === '/files/roots?scope=container') {
        const roots = [{ id: 'download', path: '/media/download', name: 'download' }];
        if (container) roots.push({ id: 'container', path: '/', name: '本地', kind: 'container' });
        return roots;
      }
      if (path.startsWith('/files/list?')) {
        const location = new URLSearchParams(path.split('?')[1]).get('path');
        if (container && location === '/')
          return { items: [{ path: '/BT_backup', name: 'BT_backup', type: 'dir' }] };
        if (container && location === '/BT_backup')
          return { items: [{ path: '/BT_backup/example.torrent', name: 'example.torrent', type: 'file' }] };
        return { items: location === '/media/download' ? [...fixture.items] : [] };
      }
      throw new Error('Unexpected request: ' + path);
    },
    ui: { settings: { directories: initialPath ? [{ source_path: initialPath }] : [] } },
    bytes: (value) => value,
    date: (value) => value,
    errorText: (error) => error.message,
  };
  const storage = globalThis.localStorage;
  globalThis.localStorage = { getItem: () => null, setItem: () => {} };
  t.after(() => {
    if (storage === undefined) delete globalThis.localStorage;
    else globalThis.localStorage = storage;
  });
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === '../api') return apiModule;
    if (name === '../features/file-browser') return browser;
    if (name.endsWith('.vue')) return { default: { name } };
    if (/^vuetify\/components\/(VTextField|VSelect|VTextarea)$/.test(name)) {
      const component = name.split('/').at(-1);
      return { [component]: { name: component } };
    }
    if (name === 'vue')
      return {
        ...vue,
        onMounted: (callback) => mounted.push(callback),
        onUnmounted: () => {},
        withDirectives: (vnode) => vnode,
      };
    throw new Error('Unexpected component dependency: ' + name);
  }, module.exports);
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup({}, { expose: () => {}, emit: () => {} }),
  );
  t.after(() => scope.stop());
  for (const callback of mounted) await callback();
  function nodes() {
    const result = [];
    function visit(node) {
      if (!node || typeof node !== 'object') return;
      if (Array.isArray(node)) return node.forEach(visit);
      result.push(node);
      visit(node.children);
    }
    visit(render({}, []));
    return result;
  }
  return {
    fixture,
    nodes,
    button: (label) => nodes().find((node) => node.props?.['aria-label'] === label),
    checkbox: (name) => nodes().find((node) => node.props?.['aria-label'] === '选择 ' + name),
    async clickName(path) {
      const name = nodes().find(
        (node) => node.props?.class === 'file-row-name' && node.props.title === path,
      );
      name.props.onClick({ stopPropagation() {} });
      await vue.nextTick();
      await vue.nextTick();
    },
  };
}

test('explicit selection toggles once by name or checkbox and exit clears its state', async (t) => {
  const files = await openFiles(t);
  assert.equal(files.checkbox('one.mkv'), undefined);
  files.button('多选文件').props.onClick();
  assert.equal(files.button('退出多选').props['aria-pressed'], true);
  await files.clickName('/media/download/one.mkv');
  assert.equal(files.checkbox('one.mkv').props.checked, true);
  files.checkbox('one.mkv').props.onChange({ target: { checked: false } });
  assert.equal(files.checkbox('one.mkv').props.checked, false);
  files.checkbox('two.mkv').props.onChange({ target: { checked: true } });
  assert.ok(files.button('整理所选 1 项'));
  files.button('退出多选').props.onClick();
  files.button('多选文件').props.onClick();
  assert.equal(files.checkbox('two.mkv').props.checked, false);
});

test('sorting preserves existing selections while refresh discards files that disappeared', async (t) => {
  const files = await openFiles(t);
  files.button('多选文件').props.onClick();
  await files.clickName('/media/download/one.mkv');
  await files.clickName('/media/download/two.mkv');
  await files.button('改为按时间排序').props.onClick();
  assert.equal(files.checkbox('one.mkv').props.checked, true);
  assert.equal(files.checkbox('two.mkv').props.checked, true);
  files.fixture.items = files.fixture.items.filter((item) => item.name !== 'two.mkv');
  await files.button('改为按名称排序').props.onClick();
  assert.equal(files.checkbox('one.mkv').props.checked, true);
  assert.equal(files.checkbox('two.mkv'), undefined);
  assert.ok(files.button('整理所选 1 项'));
  files.button('上一级').props.onClick();
  await vue.nextTick();
  assert.equal(files.button('整理所选 1 项'), undefined);
});

test('virtual mount parents navigate even in selection mode and cannot enter batch actions', async (t) => {
  const files = await openFiles(t, { initialPath: '' });
  files.button('多选文件').props.onClick();
  assert.equal(files.checkbox('media'), undefined);
  await files.clickName('/media');
  assert.equal(files.checkbox('download'), undefined);
  await files.clickName('/media/download');
  assert.ok(files.checkbox('one.mkv'));
  assert.equal(files.button('整理所选 1 项'), undefined);
});

test('container-only folders expose metadata without download or mutation controls', async (t) => {
  const files = await openFiles(t, { initialPath: '', container: true });
  assert.ok(files.button('新建目录').props.disabled);
  assert.equal(files.button('多选文件'), undefined);
  await files.clickName('/BT_backup');
  assert.ok(files.fixture.requests.some((path) => path.includes('root_id=container&path=%2FBT_backup')));
  assert.equal(files.button('删除 example.torrent'), undefined);
  await files.clickName('/BT_backup/example.torrent');
  assert.ok(files.nodes().some((node) => node.type === 'h2' && node.children === 'example.torrent'));
  assert.equal(files.button('下载文件'), undefined);
});

test('selection reconciliation excludes virtual or unsupported items and deduplicates paths', () => {
  const items = [
    { path: '/media/file.mkv', name: 'file.mkv', type: 'file' },
    { path: '/media', name: 'media', type: 'dir', virtual: true },
    { path: '/media/link', name: 'link', type: 'symlink' },
  ];
  assert.deepEqual(
    browser.retainBrowserSelection(
      ['/media/file.mkv', '/media/file.mkv', '/media', '/media/link', '/missing'],
      items,
    ),
    ['/media/file.mkv'],
  );
});
