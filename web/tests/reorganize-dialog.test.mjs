import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

// Execute the actual SFC render handlers and watchers without a browser or network.
const source = readFileSync(
  new URL('../src/components/ReorganizeDialog.vue', import.meta.url),
  'utf8',
);
const { descriptor } = parse(source);
const script = compileScript(descriptor, { id: 'organize-dialog-test', inlineTemplate: true });
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

function openDialog(t, initial = {}) {
  const requests = [];
  const apiModule = {
    ui: {
      settings: {
        directories: [
          { library_path: '/library', mode: 'copy', scraping: false, library_type_folder: false },
        ],
      },
    },
    api: async (path, body) => {
      requests.push({ path, body });
      return { items: [], total: 0, submitted: 0, failed: 0 };
    },
    label: (value) => value,
    errorText: (error) => error.message,
    image: (path) => path,
    toast: () => {},
  };
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === '../api') return apiModule;
    if (name === './FileManagerIcon.vue') return { default: {} };
    if (name === 'vue')
      return {
        ...vue,
        onMounted: () => {},
        onUnmounted: () => {},
        useId: () => 'organize-test',
        withDirectives: (vnode) => vnode,
      };
    throw new Error('Unexpected component dependency: ' + name);
  }, module.exports);
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup(
      { paths: ['/downloads/Test.mkv'], initial },
      { expose: () => {}, emit: () => {} },
    ),
  );
  t.after(() => scope.stop());
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
  const pathInput = () => nodes().find((node) => node.props?.['aria-label'] === '目的路径');
  const selects = () => nodes().filter((node) => node.type === 'select');
  const switches = () => nodes().filter((node) => node.props?.type === 'checkbox');
  return {
    requests,
    pathInput,
    selects,
    switches,
    async path(value) {
      pathInput().props.onInput({ target: { value } });
      await vue.nextTick();
    },
    async select(index, value) {
      selects()[index].props['onUpdate:modelValue'](value);
      await vue.nextTick();
    },
    async toggle(index, checked) {
      switches()[index].props.onChange({ target: { checked } });
      await vue.nextTick();
    },
    async preview() {
      const button = nodes().find((node) => node.props?.class === 'preview-action');
      await button.props.onClick();
      return requests.at(-1).body;
    },
  };
}

test('manual target can be edited and cleared without changing storage, mode, or explicit switches', async (t) => {
  const dialog = openDialog(t);
  await dialog.select(0, 'local');
  await dialog.select(1, 'hardlink');
  await dialog.toggle(0, true);
  await dialog.toggle(1, true);
  await dialog.path('/library');
  await dialog.path('/library/custom');
  assert.equal(dialog.pathInput().props.value, '/library/custom');
  assert.equal(dialog.pathInput().props.disabled, false);
  assert.equal(dialog.pathInput().props.readonly, undefined);
  assert.ok(dialog.switches().every((node) => node.props.checked));
  await dialog.path('');
  assert.equal(dialog.pathInput().props.value, '');
  const body = await dialog.preview();
  assert.equal(body.target_path, '');
  assert.equal(body.target_storage, 'local');
  assert.equal(body.mode, 'hardlink');
  assert.equal(body.library_type_folder, true);
  assert.equal(body.scraping, true);
});

test('storage and method changes preserve the custom target and explicit classification setting', async (t) => {
  const dialog = openDialog(t, { target_path: '/library/original' });
  await dialog.path('/library/custom');
  await dialog.toggle(0, true);
  await dialog.select(0, '');
  await dialog.select(1, 'copy');
  await dialog.select(1, 'hardlink');
  assert.equal(dialog.pathInput().props.value, '/library/custom');
  const body = await dialog.preview();
  assert.equal(body.target_path, '/library/custom');
  assert.equal(body.target_storage, '');
  assert.equal(body.mode, 'hardlink');
  assert.equal(body.library_type_folder, true);
});

test('choosing automatic target retains other automatic values and preserves explicit false switches', async (t) => {
  const dialog = openDialog(t);
  await dialog.path('/library');
  await dialog.toggle(0, false);
  await dialog.toggle(1, false);
  await dialog.path('自动');
  assert.equal(dialog.pathInput().props.value, '');
  const body = await dialog.preview();
  assert.equal(body.target_path, '');
  assert.equal(body.target_storage, '');
  assert.equal(body.mode, '');
  assert.equal(body.library_type_folder, false);
  assert.equal(body.scraping, false);
});
