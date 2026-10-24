import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
const { descriptor } = parse(
  readFileSync(new URL('../src/components/PluginMarketSettingDialog.vue', import.meta.url), 'utf8'),
);
const script = compileScript(descriptor, { id: 'MarketSettings' });
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;
const first = 'https://owned.invalid/a/index.json',
  second = 'https://owned.invalid/b/index.json';
function fixture({
  markets = [first],
  result = { success: true },
  readError,
  writeError,
  wait,
} = {}) {
  const requests = [],
    emitted = [],
    toasts = [];
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === 'vue') return { ...vue, onMounted: () => {} };
    if (name === 'vuetify') return { useDisplay: () => ({ smAndDown: vue.ref(false) }) };
    if (name === '../api')
      return {
        api: async (path, body, method) => {
          requests.push({ path, body, method });
          assert.equal(path, '/plugins/markets');
          if (!method) {
            if (readError) throw new Error(readError);
            return { plugin_markets: markets };
          }
          if (wait) await wait;
          if (writeError) throw new Error(writeError);
          return result;
        },
        errorText: (e) => e.message,
        toast: (text) => toasts.push(text),
      };
    if (name.startsWith('vuetify/') || name.endsWith('.vue') || name === 'vuedraggable') return {};
    throw new Error('Unexpected import ' + name);
  }, module.exports);
  const state = module.exports.default.setup(
    {},
    { expose: () => {}, emit: (...args) => emitted.push(args) },
  );
  return { state, requests, emitted, toasts };
}
test('all changed Vue templates compile, including market settings integration', () => {
  for (const name of ['PluginMarketSettingDialog', 'Plugins', 'PluginManageCard']) {
    const { descriptor: source } = parse(
      readFileSync(new URL(`../src/components/${name}.vue`, import.meta.url), 'utf8'),
    );
    const binding = compileScript(source, { id: name });
    const result = compileTemplate({
      source: source.template.content,
      filename: name + '.vue',
      id: name,
      compilerOptions: { bindingMetadata: binding.bindings },
    });
    assert.deepEqual(result.errors, []);
  }
});
test('parse newline/comma/Chinese comma, trim, stable exact dedupe and URL restrictions', () => {
  const f = fixture();
  assert.deepEqual(
    f.state.parseRepos(
      ` ${first}\n${second},${first}，file:///tmp/a，https://user:pass@owned.invalid/a，https://owned.invalid/a#x`,
    ),
    {
      repos: [first, second],
      duplicates: [first],
      invalid: ['file:///tmp/a', 'https://user:pass@owned.invalid/a', 'https://owned.invalid/a#x'],
    },
  );
});
test('list add/edit/cancel/remove/reorder and duplicate rejection retain exact source strings', async () => {
  const f = fixture();
  await f.state.load();
  f.state.newRepo.value = ' ' + second + ' ';
  f.state.addRepo();
  assert.deepEqual(f.state.repos.value, [first, second]);
  f.state.newRepo.value = first;
  f.state.addRepo();
  assert.equal(f.state.error.value, '仓库地址已存在');
  f.state.newRepo.value = '';
  f.state.moveRepo(1, -1);
  assert.deepEqual(f.state.repos.value, [second, first]);
  f.state.startEdit(0);
  f.state.editingUrl.value = first;
  f.state.saveEdit();
  assert.equal(f.state.editingIndex.value, 0);
  assert.equal(f.state.saveDisabled.value, true);
  f.state.cancelEdit();
  assert.deepEqual(f.state.repos.value, [second, first]);
  f.state.startEdit(0);
  f.state.editingUrl.value = 'https://owned.invalid/c/index.json';
  f.state.saveEdit();
  assert.equal(f.state.editingIndex.value, null);
  f.state.removeRepo(1);
  assert.deepEqual(f.state.repos.value, ['https://owned.invalid/c/index.json']);
});
test('text switching keeps original ordered normalization and reports dropped invalid entries', async () => {
  const f = fixture();
  await f.state.load();
  f.state.switchMode('text');
  assert.equal(f.state.text.value, first);
  f.state.text.value = second + ',nope，' + first + '\n' + second;
  assert.equal(f.state.saveDisabled.value, true);
  await f.state.save();
  assert.equal(f.requests.length, 1);
  f.state.switchMode('list');
  assert.deepEqual(f.state.repos.value, [second, first]);
  assert.match(f.state.warning.value, /1 个无效/);
  assert.match(f.state.warning.value, /1 个重复/);
});
test('empty list/text cannot save, and pending list edit/add must be resolved', async () => {
  const f = fixture({ markets: [] });
  await f.state.load();
  assert.equal(f.state.saveDisabled.value, true);
  await f.state.save();
  f.state.switchMode('text');
  assert.equal(f.state.saveDisabled.value, true);
  await f.state.save();
  assert.equal(f.requests.length, 1);
  f.state.switchMode('list');
  f.state.newRepo.value = first;
  f.state.addRepo();
  f.state.newRepo.value = second;
  assert.equal(f.state.saveDisabled.value, true);
});
test('successful PUT sends only ordered plugin_markets and emits saved once', async () => {
  const f = fixture();
  await f.state.load();
  f.state.switchMode('text');
  f.state.text.value = second + ',' + first + ',' + second;
  await f.state.save();
  assert.deepEqual(f.requests[1], {
    path: '/plugins/markets',
    body: { plugin_markets: [second, first] },
    method: 'PUT',
  });
  assert.deepEqual(f.emitted, [['saved']]);
  assert.equal(f.toasts.length, 1);
});
for (const options of [
  { result: { success: false, message: '不能保存' } },
  { result: {} },
  { writeError: '网络失败' },
]) {
  test(`failed PUT preserves draft and does not emit saved: ${JSON.stringify(options)}`, async () => {
    const f = fixture(options);
    await f.state.load();
    f.state.switchMode('text');
    f.state.text.value = second;
    await f.state.save();
    assert.equal(f.state.text.value, second);
    assert.notEqual(f.state.error.value, '');
    assert.deepEqual(f.emitted, []);
    assert.deepEqual(f.toasts, []);
    assert.equal(f.state.saving.value, false);
  });
}
test('read error or malformed read cannot overwrite unknown persisted settings', async () => {
  for (const options of [{ readError: '读取失败' }, { markets: 'not-an-array' }]) {
    const f = fixture(options);
    await f.state.load();
    f.state.repos.value = [second];
    await f.state.save();
    assert.equal(f.state.saveDisabled.value, true);
    assert.equal(f.requests.length, 1);
    assert.deepEqual(f.emitted, []);
  }
});
test('duplicate save while pending does not send twice and list actions remain locked', async () => {
  let finish;
  const wait = new Promise((resolve) => {
    finish = resolve;
  });
  const f = fixture({ wait });
  await f.state.load();
  const pending = f.state.save();
  await f.state.save();
  f.state.removeRepo(0);
  assert.equal(f.state.saveDisabled.value, true);
  assert.deepEqual(f.state.repos.value, [first]);
  finish();
  await pending;
  assert.equal(f.requests.length, 2);
  assert.deepEqual(f.emitted, [['saved']]);
});
