import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

const source = readFileSync(
  new URL('../src/components/EpisodeFormatRuleSettings.vue', import.meta.url),
  'utf8',
);
const { descriptor } = parse(source);
const script = compileScript(descriptor, { id: 'episode-rules-test', inlineTemplate: true });
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
const seed = {
  name: '集号',
  enabled: true,
  order: 1,
  pattern: '(?P<ep>\\d+)',
  min_file_size_mb: 0,
};
function mountRules(t, respond) {
  const requests = [],
    toasts = [];
  let mount = () => {},
    unmount = () => {};
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === '../api')
      return {
        api: async (path, body, method) => {
          requests.push({ path, body, method });
          return respond(path, body, method);
        },
        errorText: (e) => e.message,
        toast: (s) => toasts.push(s),
      };
    if (name === '../features/bounded-read') return { boundedRead: (read, signal) => read(signal) };
    if (name === 'vuedraggable') return { name: 'Draggable' };
    if (name.startsWith('vuetify/components/'))
      return Object.fromEntries(
        [
          'VCard',
          'VCardTitle',
          'VCardSubtitle',
          'VCardText',
          'VTextField',
          'VSwitch',
          'VBtn',
          'VAlert',
        ].map((key) => [key, { name: key }]),
      );
    if (name === 'vue')
      return {
        ...vue,
        onMounted: (fn) => {
          mount = fn;
        },
        onUnmounted: (fn) => {
          unmount = fn;
        },
      };
    throw new Error('Unexpected dependency: ' + name);
  }, module.exports);
  const scope = vue.effectScope();
  const render = scope.run(() => module.exports.default.setup({}, { expose() {} }));
  t.after(() => {
    unmount();
    scope.stop();
  });
  function nodes() {
    const output = [];
    function visit(node) {
      if (!node || typeof node !== 'object') return;
      if (Array.isArray(node)) return node.forEach(visit);
      output.push(node);
      if (node.type?.name === 'Draggable') {
        (node.props.modelValue || []).forEach((element, index) =>
          visit(node.children.item({ element, index })),
        );
      } else if (
        node.children &&
        !Array.isArray(node.children) &&
        typeof node.children === 'object'
      ) {
        for (const slot of Object.values(node.children))
          if (typeof slot === 'function') visit(slot());
      } else visit(node.children);
    }
    visit(render({}, []));
    return output;
  }
  return {
    requests,
    toasts,
    nodes,
    mount: async () => {
      await mount();
      await vue.nextTick();
    },
  };
}
function text(node) {
  if (!node) return '';
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(text).join('');
  if (node.children && typeof node.children.default === 'function')
    return text(node.children.default());
  return typeof node.children === 'string'
    ? node.children
    : Array.isArray(node.children)
      ? text(node.children)
      : '';
}
const button = (screen, label) =>
  screen.nodes().find((node) => node.type?.name === 'VBtn' && text(node) === label);
const fields = (screen, label) => screen.nodes().filter((node) => node.props?.label === label);

test('rule settings loads, adds, edits, saves only its own real configuration with revision', async (t) => {
  const screen = mountRules(t, async (_path, body, method) =>
    method === 'PUT'
      ? { revision: body.revision + 1, rules: body.rules }
      : { revision: 4, rules: [seed] },
  );
  assert.equal(button(screen, '添加规则').props.disabled, true);
  const help = screen
    .nodes()
    .filter((node) => node.props?.class === 'episode-rule-help')
    .map(text)
    .join('');
  assert.ok(help.includes('未命中时根据样本智能分析'));
  assert.ok(help.includes('已有规则全部停用时仍可智能分析'));
  assert.ok(!help.includes('自动推导尚未实现'));
  await screen.mount();
  await button(screen, '添加规则').props.onClick();
  assert.equal(fields(screen, '最小文件大小（MB）')[1].props.modelValue, 500);
  fields(screen, '规则名称')[1].props['onUpdate:modelValue']('第二条');
  fields(screen, '正则表达式')[1].props['onUpdate:modelValue']('E(?<ep>\\d+)');
  await button(screen, '保存').props.onClick();
  const request = screen.requests.at(-1);
  assert.equal(request.method, 'PUT');
  assert.equal(request.path, '/files/episode-format/rules');
  assert.deepEqual(request.body, {
    revision: 4,
    rules: [
      seed,
      { name: '第二条', pattern: 'E(?<ep>\\d+)', enabled: true, order: 2, min_file_size_mb: 500 },
    ],
  });
  assert.ok(screen.toasts.includes('集数定位规则保存成功'));
  await button(screen, '保存').props.onClick();
  assert.equal(screen.requests.at(-1).body.revision, 5);
});

test('drag reorder persists order and delete can save an empty table without touching recognition words', async (t) => {
  const screen = mountRules(t, async (_path, body, method) =>
    method === 'PUT'
      ? { revision: body.revision + 1, rules: body.rules }
      : { revision: 1, rules: [seed, { ...seed, name: '先匹配', order: 2 }] },
  );
  await screen.mount();
  const drag = () => screen.nodes().find((node) => node.type?.name === 'Draggable');
  drag().props['onUpdate:modelValue']([...drag().props.modelValue].reverse());
  await drag().props.onEnd();
  assert.equal(screen.requests.at(-1).body.rules[0].name, '先匹配');
  assert.deepEqual(
    screen.requests.at(-1).body.rules.map((rule) => rule.order),
    [1, 2],
  );
  for (let i = 0; i < 2; i++)
    screen
      .nodes()
      .find((node) => node.props?.['aria-label'] === '删除规则 1')
      .props.onClick();
  await button(screen, '保存').props.onClick();
  assert.deepEqual(screen.requests.at(-1).body.rules, []);
  assert.ok(screen.requests.every((request) => request.path === '/files/episode-format/rules'));
});

test('invalid fields and failed saves retain editable drafts and never claim success', async (t) => {
  const screen = mountRules(t, async (_path, _body, method) => {
    if (method === 'PUT') throw new Error('集数定位规则已变化，请重新载入后保存');
    return { revision: 1, rules: [seed] };
  });
  await screen.mount();
  fields(screen, '规则名称')[0].props['onUpdate:modelValue']('');
  await button(screen, '保存').props.onClick();
  assert.equal(screen.requests.length, 1);
  fields(screen, '规则名称')[0].props['onUpdate:modelValue']('保留草稿');
  await button(screen, '保存').props.onClick();
  assert.equal(fields(screen, '规则名称')[0].props.modelValue, '保留草稿');
  assert.ok(screen.nodes().some((node) => text(node).includes('规则已变化')));
  assert.equal(screen.toasts.length, 0);
});
