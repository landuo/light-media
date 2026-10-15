import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as fileBrowser from '../src/features/file-browser.ts';

const source = readFileSync(
  new URL('../src/components/DirectoryPathPicker.vue', import.meta.url),
  'utf8',
);
const { descriptor } = parse(source);
const script = compileScript(descriptor, {
  id: 'directory-path-picker-test',
  inlineTemplate: true,
});
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

function createPicker(t, options = {}) {
  const requests = [],
    events = [];
  const roots = [{ id: 'library', path: '/media/library', name: '媒体库', kind: 'library' }];
  const listings = {
    '/media/library': [
      { name: '电影', path: '/media/library/电影', type: 'dir' },
      { name: 'Test.mkv', path: '/media/library/Test.mkv', type: 'file' },
    ],
    '/media/library/电影': [{ name: 'Test', path: '/media/library/电影/Test', type: 'dir' }],
  };
  let rootFailure = options.rootFailure || false;
  let listFailure = options.listFailure || false;
  const props = vue.reactive({
    modelValue: options.path || '',
    suggestions: ['/media/library'],
    disabled: false,
  });
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === '../api')
      return {
        errorText: (error) => error.message,
        api: async (url) => {
          requests.push(url);
          if (url === '/files/roots') {
            if (
              options.firstRootResponse &&
              requests.filter((request) => request === '/files/roots').length === 1
            )
              return options.firstRootResponse();
            if (rootFailure) throw new Error('根目录加载失败');
            return roots;
          }
          const parsed = new URL(url, 'http://fixture.invalid');
          assert.equal(parsed.pathname, '/files/list');
          assert.equal(parsed.searchParams.get('root_id'), 'library');
          const path = parsed.searchParams.get('path');
          assert.ok(path === '/media/library' || path.startsWith('/media/library/'));
          if (listFailure) throw new Error('目录读取失败');
          return { items: listings[path] || [] };
        },
      };
    if (name === './FileManagerIcon.vue') return { default: {} };
    if (name === '../features/file-browser') return fileBrowser;
    if (name === 'vue') return { ...vue, onUnmounted: () => {} };
    throw new Error('Unexpected dependency: ' + name);
  }, module.exports);
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup(props, {
      expose: () => {},
      emit: (event, path) => {
        events.push({ event, path });
        props.modelValue = path;
      },
    }),
  );
  t.after(() => scope.stop());
  const previousWindow = globalThis.window;
  globalThis.window = { innerWidth: 1200, innerHeight: 900 };
  t.after(() => {
    globalThis.window = previousWindow;
  });
  let focusCount = 0;
  const trigger = {
    getBoundingClientRect: () => ({ right: 800, bottom: 200 }),
    focus: () => focusCount++,
  };
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
  const findLabel = (label) => nodes().find((node) => node.props?.['aria-label'] === label);
  return {
    props,
    requests,
    events,
    nodes,
    findLabel,
    async open() {
      await findLabel('浏览目的目录').props.onClick({
        currentTarget: trigger,
        stopPropagation: () => {},
      });
      await vue.nextTick();
    },
    async click(label) {
      const node = findLabel(label);
      assert.ok(node, `Expected control ${label}`);
      await node.props.onClick();
      await vue.nextTick();
    },
    async retry() {
      const button = nodes().find((node) => node.type === 'button' && node.children === '重试');
      assert.ok(button);
      rootFailure = false;
      listFailure = false;
      await button.props.onClick();
      await vue.nextTick();
    },
    get focusCount() {
      return focusCount;
    },
  };
}

test('picker opens without changing a typed path, reveals it and lists only directories', async (t) => {
  const picker = createPicker(t, { path: '/media/library/电影' });
  await picker.open();
  assert.deepEqual(picker.events, []);
  assert.ok(picker.findLabel('选择目录 /media/library/电影'));
  assert.equal(picker.findLabel('选择目录 /media/library/Test.mkv'), undefined);
  assert.equal(picker.requests.filter((url) => url.startsWith('/files/list')).length, 1);
  await picker.click('选择目录 /media/library/电影');
  assert.equal(picker.props.modelValue, '/media/library/电影');
  assert.ok(picker.findLabel('选择目录 /media/library/电影/Test'));
  assert.ok(picker.findLabel('选择目的目录'), 'selection keeps tree open for deeper browsing');
});

test('virtual parents are navigation only; selecting a real directory updates immediately', async (t) => {
  const picker = createPicker(t);
  await picker.open();
  await picker.click('浏览目录 /media');
  assert.deepEqual(picker.events, []);
  assert.equal(
    picker.requests.length,
    1,
    'virtual navigation must not request unauthorized parents',
  );
  await picker.click('选择目录 /media/library');
  assert.deepEqual(picker.events, [{ event: 'update:modelValue', path: '/media/library' }]);
  assert.ok(picker.findLabel('选择目录 /media/library/电影'));
});

test('free typed paths outside the tree remain unchanged and never become browse permissions', async (t) => {
  const picker = createPicker(t, { path: '/unmounted/new-library' });
  await picker.open();
  assert.equal(picker.props.modelValue, '/unmounted/new-library');
  assert.deepEqual(picker.events, []);
  assert.deepEqual(picker.requests, ['/files/roots']);
});

test('root and directory failures expose working retry actions', async (t) => {
  const picker = createPicker(t, { rootFailure: true, listFailure: true });
  await picker.open();
  assert.ok(picker.nodes().some((node) => node.props?.role === 'alert'));
  await picker.retry();
  await picker.click('浏览目录 /media');
  await picker.click('选择目录 /media/library');
  assert.ok(picker.findLabel('选择目录 /media/library/电影'));
});

test('directory read errors can be retried without selecting a different path', async (t) => {
  const picker = createPicker(t, { listFailure: true });
  await picker.open();
  await picker.click('浏览目录 /media');
  await picker.click('选择目录 /media/library');
  assert.ok(picker.nodes().some((node) => node.props?.role === 'alert'));
  await picker.retry();
  assert.equal(picker.props.modelValue, '/media/library');
  assert.ok(picker.findLabel('选择目录 /media/library/电影'));
});

test('Escape and explicit close dismiss the picker and restore trigger focus', async (t) => {
  const picker = createPicker(t);
  await picker.open();
  const dialog = picker.findLabel('选择目的目录');
  dialog.props.onKeydown({ key: 'Escape', stopPropagation: () => {}, preventDefault: () => {} });
  await vue.nextTick();
  assert.equal(picker.findLabel('选择目的目录'), undefined);
  assert.equal(picker.focusCount, 1);
  await picker.open();
  await picker.click('关闭目录选择');
  assert.equal(picker.findLabel('选择目的目录'), undefined);
  assert.equal(picker.focusCount, 2);
});

test('a late response from a closed picker cannot replace a newly opened tree', async (t) => {
  let release;
  let requested;
  const firstRequested = new Promise((resolve) => {
    requested = resolve;
  });
  const firstResponse = new Promise((resolve) => {
    release = resolve;
  });
  const picker = createPicker(t, {
    firstRootResponse: () => {
      requested();
      return firstResponse;
    },
  });
  const firstOpen = picker.open();
  await firstRequested;
  await picker.click('关闭目录选择');
  await picker.open();
  release([{ id: 'stale', path: '/old-root', name: '旧目录' }]);
  await firstOpen;
  assert.ok(picker.findLabel('浏览目录 /media'));
  assert.equal(picker.findLabel('浏览目录 /old-root'), undefined);
  assert.deepEqual(picker.events, []);
});

test('Tab and Shift+Tab stay inside the picker and skip hidden or disabled controls', async (t) => {
  const picker = createPicker(t);
  await picker.open();
  const dialog = picker.findLabel('选择目的目录');
  let focused;
  const control = (name, { hidden = false, disabled = false } = {}) =>
    vue.markRaw({
      name,
      disabled,
      getClientRects: () => (hidden ? [] : [{}]),
      focus() {
        focused = this;
      },
    });
  const first = control('close');
  const hidden = control('hidden', { hidden: true });
  const disabled = control('loading directory', { disabled: true });
  const middle = control('automatic');
  const last = control('directory');
  const panel = vue.markRaw({
    querySelectorAll(selector) {
      assert.equal(selector, 'button:not(:disabled)');
      return [first, hidden, disabled, middle, last].filter((button) => !button.disabled);
    },
    focus() {
      focused = this;
    },
  });
  dialog.props.ref.value = panel;
  const tab = (target, shiftKey = false) => {
    let prevented = false;
    let stopped = false;
    dialog.props.onKeydown({
      key: 'Tab',
      target,
      shiftKey,
      preventDefault() {
        prevented = true;
      },
      stopPropagation() {
        stopped = true;
      },
    });
    assert.ok(prevented && stopped, 'the underlying dialog must not receive Tab');
  };
  tab(panel);
  assert.equal(focused, first, 'Tab from the panel enters its first button');
  tab(panel, true);
  assert.equal(focused, last, 'Shift+Tab from the panel enters its last button');
  tab(first);
  assert.equal(focused, middle, 'hidden and disabled controls are skipped');
  tab(last);
  assert.equal(focused, first, 'Tab wraps from last to first');
  tab(first, true);
  assert.equal(focused, last, 'Shift+Tab wraps from first to last');
  tab(middle, true);
  assert.equal(focused, first);
  assert.deepEqual(picker.events, [], 'keyboard navigation never changes the destination');
});

test('Tab stays on the panel if no visible enabled controls are available', async (t) => {
  const picker = createPicker(t);
  await picker.open();
  const dialog = picker.findLabel('选择目的目录');
  let focused = false;
  const panel = vue.markRaw({
    querySelectorAll: () => [],
    focus: () => {
      focused = true;
    },
  });
  dialog.props.ref.value = panel;
  dialog.props.onKeydown({ key: 'Tab', target: panel, preventDefault() {}, stopPropagation() {} });
  assert.ok(focused);
});
