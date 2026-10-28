// Actual Vue component mount; the renderer/transport history are synthetic.
// This proves ordinary note presentation, never SDK Notification.note delivery.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import * as vue from 'vue';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import MarkdownIt from 'markdown-it';
import linkAttributes from 'markdown-it-link-attributes';
import * as helpers from '../src/features/plugin-web-message.ts';
const source = JSON.parse(
  fs.readFileSync(
    new URL('../../testdata/standard-web-message/frontend-source-oracle.json', import.meta.url),
  ),
);
class Element {
  constructor(tag, text = '') {
    this.tag = tag;
    this.text = text;
    this.children = [];
    this.props = {};
    this.parentElement = null;
    this.scrollHeight = 0;
    this.clientHeight = 0;
    this.scrollTop = 0;
  }
  addEventListener() {}
  removeEventListener() {}
  closest() {
    return null;
  }
}
const renderer = vue.createRenderer({
  createElement: (tag) => new Element(tag),
  createText: (text) => new Element('#text', text),
  createComment: (text) => new Element('#comment', text),
  insert(node, parent, anchor) {
    if (node.parentElement) {
      const old = node.parentElement.children;
      old.splice(old.indexOf(node), 1);
    }
    node.parentElement = parent;
    const index = anchor ? parent.children.indexOf(anchor) : -1;
    if (index < 0) parent.children.push(node);
    else parent.children.splice(index, 0, node);
  },
  remove(node) {
    const list = node.parentElement?.children;
    if (list) list.splice(list.indexOf(node), 1);
  },
  setText(node, text) {
    node.text = text;
  },
  setElementText(node, text) {
    node.text = text;
    node.children = [];
  },
  parentNode: (node) => node.parentElement,
  nextSibling: (node) => {
    const list = node.parentElement?.children || [];
    return list[list.indexOf(node) + 1] || null;
  },
  patchProp(node, key, _old, value) {
    node.props[key] = value;
  },
});
const stubCache = new Map();
function stub(name) {
  if (stubCache.has(name)) return stubCache.get(name);
  const component = {
    name,
    setup(_, { slots, attrs, emit }) {
      if (name === 'VInfiniteScroll') vue.onMounted(() => emit('load', { done() {} }));
      return () =>
        vue.h(
          'owned-' + name.toLowerCase(),
          { 'data-component': name, ...attrs },
          slots.default?.(),
        );
    },
  };
  stubCache.set(name, component);
  return component;
}
const components = new Proxy({}, { get: (_, name) => stub(name) });
const windowBoundary = {
  setTimeout() {
    return 1;
  },
  clearTimeout() {},
  getComputedStyle: () => ({ overflowY: 'auto' }),
  open() {},
};
function compile(raw, history) {
  const { descriptor } = parse(raw),
    script = compileScript(descriptor, { id: 'NoteSourceMount', inlineTemplate: true }),
    code = ts.transpileModule(script.content, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText,
    module = { exports: {} };
  const sourceEmpty = new Function(source.sourceIsEmptyObject + ';return isNullOrEmptyObject')();
  new Function('require', 'exports', 'ref', 'window', 'HTMLElement', 'requestAnimationFrame', code)(
    (path) => {
      if (path === 'vue') return vue;
      if (path === 'vuetify') return { useDisplay: () => ({ mdAndUp: vue.ref(true) }) };
      if (path.startsWith('vuetify/')) return components;
      if (path.endsWith('.vue')) return { __esModule: true, default: stub('OwnedClose') };
      if (path === '../api') return { api: async () => history, errorText: (e) => e.message };
      if (path === '../features/plugin-web-message') return helpers;
      if (path === '../features/system-messages') return { systemMessageRelativeTime: () => '' };
      if (path === 'markdown-it') return { __esModule: true, default: MarkdownIt };
      if (path === 'markdown-it-link-attributes')
        return { __esModule: true, default: linkAttributes };
      if (path === '@/@core/utils') return { isNullOrEmptyObject: sourceEmpty };
      if (path === '@core/utils/formatters') return { formatDateDifference: () => '' };
      throw Error('unhandled SFC boundary ' + path);
    },
    module.exports,
    vue.ref,
    windowBoundary,
    Element,
    (fn) => {
      fn();
      return 1;
    },
  );
  return module.exports.default;
}
function find(node, name) {
  if (node.props['data-component'] === name) return node;
  for (const child of node.children) {
    const hit = find(child, name);
    if (hit) return hit;
  }
  return null;
}
function text(node) {
  return (node.tag === '#comment' ? '' : node.text) + node.children.map(text).join('');
}
test('component-synthetic: ordinary history note mounts the same list as the fixed MessageCard', async (t) => {
  const note = JSON.stringify([
    { title_year: 'Owned Film (2026)', type: '电影', vote_average: 8.4 },
    {
      enclosure: 'https://owned.invalid/torrent',
      title: 'Owned Release',
      volume_factor: 0,
      seeders: 12,
      description: 'Actual synthetic history note',
    },
  ]);
  const message = { id: 901, action: 1, note, reg_time: '2026-10-26 10:00:00' };
  const fixed = compile(source.messageCardSource),
    target = compile(
      fs.readFileSync(
        new URL('../src/components/ShortcutMessageDialog.vue', import.meta.url),
        'utf8',
      ),
      [message],
    );
  const one = new Element('root'),
    two = new Element('root');
  const appOne = renderer.createApp(fixed, { message }),
    appTwo = renderer.createApp(target, { modelValue: true });
  for (const app of [appOne, appTwo])
    for (const name of [
      'VCard',
      'VCardTitle',
      'VCardText',
      'VList',
      'VListItem',
      'VListItemTitle',
      'VListItemSubtitle',
      'VImg',
      'VSkeletonLoader',
    ])
      app.component(name, stub(name));
  appOne.mount(one);
  appTwo.mount(two);
  t.after(() => {
    appOne.unmount();
    appTwo.unmount();
  });
  await vue.nextTick();
  await new Promise(setImmediate);
  await vue.nextTick();
  const sourceList = find(one, 'VList'),
    targetList = find(two, 'VList');
  assert.ok(sourceList);
  assert.ok(targetList);
  const clean = (s) => s.replace(/\s+/g, ' ').trim();
  function blocks(node) {
    const out = [];
    if (['VListItemTitle', 'VListItemSubtitle'].includes(node.props['data-component']))
      out.push(clean(text(node)));
    else for (const child of node.children) out.push(...blocks(child));
    return out;
  }
  assert.deepEqual(blocks(targetList), blocks(sourceList));
  assert.deepEqual(blocks(targetList), [
    '1. Owned Film (2026)',
    '类型：电影 评分：8.4',
    '2. Owned Release 0 ↑12',
    'Actual synthetic history note',
  ]);
});
