import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

// Execute the actual Settings SFC setup/render and its event handlers. Only its
// child components and API boundary are stubbed; no network or real settings.
const copy = (value) => JSON.parse(JSON.stringify(value));
const { descriptor } = parse(
  readFileSync(new URL('../src/components/Settings.vue', import.meta.url), 'utf8'),
);
const script = compileScript(descriptor, { id: 'Settings', inlineTemplate: true });
const source = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

function descendants(node) {
  if (node == null || typeof node !== 'object') return [];
  if (Array.isArray(node)) return node.flatMap(descendants);
  return [node, ...descendants(node.children)];
}
function nodeText(node) {
  if (node == null) return '';
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(nodeText).join(' ');
  return nodeText(node.children);
}
async function mount(t, initial = {}) {
  let settings = {
    tmdb: { api_key: '', access_token: '' },
    fnos: { host: '', username: '', password: '', enabled: false },
    sites: [],
    downloaders: [{ id: 'synthetic-downloader' }],
    directories: [{ id: 'synthetic-directory' }],
    secret_state: { fnos: false },
    site_data_refresh_hours: 6,
    read_site_messages: null,
    effective_read_site_messages: false,
    ...copy(initial),
  };
  const fixture = { requests: [], toasts: [], failSave: false, holdSave: null, response: null };
  const mounted = [], unmounted = [], pending = [];
  const location = { hash: '#settings?tab=system' };
  const ui = { settings: copy(settings) };
  const toast = (message, error = false) => fixture.toasts.push({ message, error });
  const action = (callback) => {
    const operation = (async () => {
      try { return await callback(); }
      catch (error) { toast(error.message, true); }
    })();
    pending.push(operation);
    return operation;
  };
  const api = async (path, body, method) => {
    fixture.requests.push({ path, body: body === undefined ? undefined : copy(body), method });
    if (path === '/settings/logging') return { level: 'INFO', debug_overrides: [] };
    assert.equal(path, '/settings', 'no unmocked network API');
    assert.equal(method, 'PUT');
    if (fixture.holdSave) await fixture.holdSave;
    if (fixture.failSave) throw new Error('合成保存失败');
    settings = { ...copy(body), secret_state: { fnos: false }, ...fixture.response };
    return copy(settings);
  };
  const module = { exports: {} };
  new Function('require', 'exports', 'window', 'location', source)(
    (dependency) => {
      if (dependency === 'vue') return {
        ...vue,
        onMounted: (callback) => mounted.push(callback),
        onBeforeUnmount: (callback) => unmounted.push(callback),
        withDirectives: (node, directives) => ({ ...node, testDirectives: directives }),
      };
      if (dependency === '../api') return {
        api, action, toast, ui,
        refreshSettings: async () => copy(settings),
        errorText: (error) => error.message,
        date: String, bytes: String,
      };
      if (dependency.endsWith('.vue')) return { name: dependency };
      if (dependency === 'vuetify/components/VSwitch') return { VSwitch: 'source-switch' };
      throw new Error('Unexpected dependency ' + dependency);
    },
    module.exports,
    { addEventListener() {}, removeEventListener() {} },
    location,
  );
  const scope = vue.effectScope();
  const render = scope.run(() => module.exports.default.setup({}, { expose() {}, emit() {} }));
  t.after(() => { unmounted.forEach((callback) => callback()); scope.stop(); });
  mounted.forEach((callback) => callback());
  await Promise.all(pending);
  await vue.nextTick();
  const nodes = () => descendants(render({}, []));
  const panel = (title) => nodes().find((n) => n.type === 'section' && nodeText(n).includes(title));
  const field = () => descendants(panel('应用地址')).find((n) => n.type === 'input');
  const saveButton = (title = '应用地址') => descendants(panel(title)).find(
    (n) => n.type === 'button' && nodeText(n).trim() === '保存',
  );
  return {
    fixture, ui, field, saveButton,
    value: () => field().testDirectives.find(([directive]) => directive === vue.vModelText)[1],
    saved: () => copy(settings),
    writes: () => fixture.requests.filter((r) => r.path === '/settings' && r.method === 'PUT'),
    updateCurrent: (delta) => { settings = { ...settings, ...copy(delta) }; },
    async edit(value) { field().props['onUpdate:modelValue'](value); await vue.nextTick(); },
    async save(title) { await saveButton(title).props.onClick(); await vue.nextTick(); },
    async tab(title) {
      const button = nodes().find((n) => n.type === 'button' && nodeText(n).trim() === title);
      assert.ok(button, title);
      button.props.onClick();
      await vue.nextTick();
    },
  };
}

test('app domain loads, saves verbatim, and reloads without introducing URL validation', async (t) => {
  const app = await mount(t, { app_domain: 'https://old.example.invalid/base/' });
  assert.equal(app.value(), 'https://old.example.invalid/base/');
  assert.equal(app.field().props.type, 'text');
  assert.equal(app.field().props.required, undefined);
  for (const value of ['https://new.example.invalid/base/', 'new.example.invalid/base', '']) {
    await app.edit(value);
    await app.save();
    assert.equal(app.writes().at(-1).body.app_domain, value);
    assert.equal(app.value(), value);
    assert.equal(app.ui.settings.app_domain, value);
    const reloaded = await mount(t, app.saved());
    assert.equal(reloaded.value(), value);
  }
});

test('missing app domain displays empty and stays optional', async (t) => {
  const app = await mount(t);
  assert.equal(app.value(), '');
  await app.save();
  assert.equal(app.writes()[0].body.app_domain, '');
});

test('domain save preserves current other sections and nullable source preference', async (t) => {
  const app = await mount(t, {
    app_domain: 'old.example.invalid',
    read_site_messages_error: '合成来源状态不可确认',
  });
  await app.edit('next.example.invalid');
  app.updateCurrent({
    downloaders: [{ id: 'new-synthetic-downloader' }],
    directories: [{ id: 'new-synthetic-directory' }],
    tmdb: { api_key: '', access_token: '' },
    site_data_refresh_hours: 24,
  });
  await app.save();
  const body = app.writes()[0].body;
  assert.equal(body.app_domain, 'next.example.invalid');
  assert.deepEqual(body.downloaders, [{ id: 'new-synthetic-downloader' }]);
  assert.deepEqual(body.directories, [{ id: 'new-synthetic-directory' }]);
  assert.equal(body.site_data_refresh_hours, 24);
  assert.equal(body.read_site_messages, null);
  assert.equal(body.read_site_messages_error, '合成来源状态不可确认');
  assert.equal('secret_state' in body, false);
});

test('unrelated saves keep the latest domain, not the unsaved draft; tabs retain the draft', async (t) => {
  const app = await mount(t, { app_domain: 'old.example.invalid' });
  await app.edit('draft.example.invalid');
  await app.tab('目录');
  await app.tab('系统');
  assert.equal(app.value(), 'draft.example.invalid');
  app.updateCurrent({ app_domain: 'external.example.invalid' });
  await app.save('TMDB 元数据');
  assert.equal(app.writes()[0].body.app_domain, 'external.example.invalid');
  assert.equal(app.value(), 'draft.example.invalid');
});

test('failed save preserves the draft and old saved setting, and permits retry', async (t) => {
  const app = await mount(t, { app_domain: 'old.example.invalid' });
  await app.edit('draft.example.invalid');
  app.fixture.failSave = true;
  await app.save();
  assert.equal(app.value(), 'draft.example.invalid');
  assert.equal(app.saved().app_domain, 'old.example.invalid');
  assert.equal(app.ui.settings.app_domain, 'old.example.invalid');
  assert.equal(app.saveButton().props.disabled, false);
  assert.deepEqual(app.fixture.toasts.at(-1), { message: '合成保存失败', error: true });
  app.fixture.failSave = false;
  await app.save();
  assert.equal(app.saved().app_domain, 'draft.example.invalid');
});

test('pending save disables submissions and guards duplicate handler calls', async (t) => {
  const app = await mount(t, { app_domain: 'old.example.invalid' });
  let release;
  app.fixture.holdSave = new Promise((resolve) => { release = resolve; });
  await app.edit('draft.example.invalid');
  const first = app.save();
  try {
    await vue.nextTick();
    assert.equal(app.saveButton().props.disabled, true);
    assert.equal(app.saveButton('TMDB 元数据').props.disabled, true);
    await app.save();
    assert.equal(app.writes().length, 1);
  } finally { release(); await first; }
  assert.equal(app.saveButton().props.disabled, false);
  assert.equal(app.saved().app_domain, 'draft.example.invalid');
});

test('saved form uses the server response, including an omitted optional domain', async (t) => {
  const app = await mount(t);
  await app.edit('draft.example.invalid');
  app.fixture.response = { app_domain: 'https://canonical.example.invalid/' };
  await app.save();
  assert.equal(app.value(), 'https://canonical.example.invalid/');
  app.fixture.response = { app_domain: undefined };
  await app.edit('');
  await app.save();
  assert.equal(app.value(), '');
});
