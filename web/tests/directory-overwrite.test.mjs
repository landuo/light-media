import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
const { descriptor } = parse(
  readFileSync(new URL('../src/components/DirectoryManager.vue', import.meta.url), 'utf8'),
);
const script = compileScript(descriptor, { id: 'directory-overwrite-test', inlineTemplate: true });
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
async function manager(t, mode, known) {
  const calls = [],
    mounts = [];
  const original = {
    id: 'owned',
    name: 'Owned directory',
    source_path: '/owned/download',
    library_path: '/owned/library',
    mode: 'hardlink',
    monitor_type: 'monitor',
    enabled: true,
    overwrite_mode: mode,
    overwrite_mode_known: known,
  };
  const api = async (path, body, method) => {
    calls.push({ path, body, method });
    return { directories: (body?.directories || [original]).map((config) => ({ config })) };
  };
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === '../api')
      return {
        api,
        action: async (fn) => fn(),
        clone: structuredClone,
        errorText: (e) => e.message,
        refreshSettings: async () => {},
        toast: () => {},
      };
    if (/^vuetify\/components\/(VTextField|VSelect|VTextarea)$/.test(name)) {
      const component = name.split('/').at(-1);
      return { [component]: { name: component } };
    }
    if (name === 'vue')
      return { ...vue, onMounted: (fn) => mounts.push(fn), withDirectives: (vnode) => vnode };
    if (name === './AppIcon.vue' || name === './PathPicker.vue') return { default: {} };
    throw new Error(name);
  }, module.exports);
  const scope = vue.effectScope();
  const render = scope.run(() => module.exports.default.setup({}, { expose() {}, emit() {} }));
  t.after(() => scope.stop());
  for (const mount of mounts) await mount();
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
  const overwrite = () =>
    nodes().find(
      (node) =>
        node.type?.name === 'VSelect' && node.props?.items?.some((item) => item.value === 'latest'),
    );
  async function save() {
    await nodes()
      .find((node) => node.type === 'form')
      .props.onSubmit({ preventDefault() {}, stopPropagation() {} });
    await vue.nextTick();
    return calls.at(-1).body.directories[0];
  }
  return { calls, overwrite, save, nodes };
}
test('directory card uses original four labels and saves each selected policy through actual form handler', async (t) => {
  const form = await manager(t, null, false);
  assert.deepEqual(
    form.overwrite().props.items.map((item) => item.title),
    ['请选择', '从不', '总是', '按文件大小', '仅保留最新'],
  );
  for (const mode of ['never', 'always', 'size', 'latest']) {
    for (const handler of [form.overwrite().props['onUpdate:modelValue']].flat()) handler(mode);
    await vue.nextTick();
    const saved = await form.save();
    assert.equal(saved.overwrite_mode, mode);
    assert.equal(saved.overwrite_mode_known, true);
    assert.ok(
      [form.overwrite().props['onUpdate:modelValue']]
        .flat()
        .every((handler) => typeof handler === 'function'),
    );
  }
});
test('loading and saving an unmigrated directory does not invent always or never', async (t) => {
  const form = await manager(t, undefined, false);
  const saved = await form.save();
  assert.equal(saved.overwrite_mode, null);
  assert.equal(saved.overwrite_mode_known, false);
});
test('an explicit source None marker survives unrelated directory edits', async (t) => {
  const form = await manager(t, null, true);
  const saved = await form.save();
  assert.equal(saved.overwrite_mode, null);
  assert.equal(saved.overwrite_mode_known, true);
});

test('adding a native directory uses source None while preserving an existing unmigrated marker', async (t) => {
  const form = await manager(t, undefined, false);
  const add = form
    .nodes()
    .find((node) => node.type === 'button' && node.props?.onClick?.name === 'add');
  add.props.onClick();
  await vue.nextTick();
  const sources = form.nodes().filter((node) => node.props?.label === '资源目录');
  sources.at(-1).props['onUpdate:modelValue']('/owned/new-download');
  await vue.nextTick();
  await form.save();
  const saved = form.calls.at(-1).body.directories;
  assert.equal(saved[0].overwrite_mode_known, false);
  assert.equal(saved[1].overwrite_mode, null);
  assert.equal(saved[1].overwrite_mode_known, true);
});
