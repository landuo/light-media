import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
const source = ts.transpileModule(
  compileScript(
    parse(
      readFileSync(
        new URL('../src/components/RecognitionWordsSettings.vue', import.meta.url),
        'utf8',
      ),
    ).descriptor,
    { id: 'SharedWords', inlineTemplate: true },
  ).content,
  { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
).outputText;
async function mount(t, value) {
  const fixture = { value, requests: [], toasts: [], fail: false };
  const mounted = [];
  const module = { exports: {} };
  new Function('require', 'exports', source)((name) => {
    if (name === 'vue') return { ...vue, onMounted: (fn) => mounted.push(fn) };
    if (name.startsWith('vuetify/')) return new Proxy({}, { get: (_, key) => ({ name: key }) });
    if (name === '../api')
      return {
        api: async (path, body, method) => {
          fixture.requests.push({ path, body, method });
          if (method === 'PUT') {
            if (fixture.fail) throw Error('owned save failed');
            return { success: true };
          }
          return { value: fixture.value };
        },
        errorText: (e) => e.message,
        toast: (...args) => fixture.toasts.push(args),
      };
    throw Error('unexpected import ' + name);
  }, module.exports);
  const scope = vue.effectScope();
  const render = scope.run(() => module.exports.default.setup({}, { expose() {} }));
  t.after(() => scope.stop());
  await Promise.all(mounted.map((fn) => fn()));
  await vue.nextTick();
  function nodes() {
    const out = [];
    function visit(n) {
      if (!n) return;
      if (Array.isArray(n)) {
        n.forEach(visit);
        return;
      }
      if (typeof n !== 'object') return;
      out.push(n);
      if (n.children?.default) visit(n.children.default());
      else visit(n.children);
    }
    visit(render({}, []));
    return out;
  }
  const find = (name) => nodes().find((n) => n.type?.name === name);
  const buttons = () => nodes().filter((n) => n.type?.name === 'VBtn');
  return {
    fixture,
    nodes,
    find,
    async edit(text) {
      find('VTextarea').props['onUpdate:modelValue'](text);
      await vue.nextTick();
    },
    async save() {
      await buttons()[0].props.onClick();
      await vue.nextTick();
    },
    async reload() {
      await buttons()[1].props.onClick();
      await vue.nextTick();
    },
    buttons,
  };
}
test('source single words card joins and submits raw split text without trimming', async (t) => {
  const a = await mount(t, ['Raw => Before', '', '  ', '# comment']);
  assert.equal(a.find('VTextarea').props.modelValue, 'Raw => Before\n\n  \n# comment');
  await a.edit('Raw => After\n\n  \n# kept');
  await a.save();
  assert.deepEqual(a.fixture.requests[1], {
    path: '/settings/recognition-words',
    body: { text: 'Raw => After\n\n  \n# kept' },
    method: 'PUT',
  });
  assert.equal(a.fixture.toasts.length, 1);
});
test('null/falsey source value is an empty editable card', async (t) => {
  for (const v of [null, false, 0, '']) {
    const a = await mount(t, v);
    assert.equal(a.find('VTextarea').props.modelValue, '');
    await a.save();
    assert.equal(a.fixture.requests[1].body.text, '');
  }
});
test('truthy non-list is not coerced but remains explicitly repairable', async (t) => {
  const a = await mount(t, { 'Raw => First': 1 });
  assert.equal(a.find('VTextarea').props.modelValue, '');
  assert.equal(a.buttons()[0].props.disabled, false);
  assert.equal(a.fixture.requests.length, 1);
  assert.ok(a.find('VAlert'));
  await a.edit('Raw => ExplicitRepair');
  await a.save();
  assert.equal(a.fixture.requests[1].body.text, 'Raw => ExplicitRepair');
  a.fixture.value = ['Raw => Repaired'];
  await a.reload();
  assert.equal(a.find('VTextarea').props.modelValue, 'Raw => Repaired');
});
test('save failure preserves draft and never reports success', async (t) => {
  const a = await mount(t, ['Raw => Before']);
  await a.edit('Raw => Draft');
  a.fixture.fail = true;
  await a.save();
  assert.equal(a.find('VTextarea').props.modelValue, 'Raw => Draft');
  assert.equal(a.fixture.toasts.length, 0);
  assert.ok(a.find('VAlert'));
  a.fixture.fail = false;
  await a.save();
  assert.equal(a.fixture.toasts.length, 1);
});
