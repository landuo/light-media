import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { parse, compileScript, compileTemplate } from "@vue/compiler-sfc";
import ts from "typescript";
import * as vue from "vue";

const transpile = (source) =>
  ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
const { descriptor } = parse(
  readFileSync(
    new URL("../src/components/Plugins.vue", import.meta.url),
    "utf8",
  ),
);
const compiled = transpile(
  compileScript(descriptor, { id: "Plugins" }).content,
);
const apiSource = transpile(
  readFileSync(new URL("../src/api.ts", import.meta.url), "utf8"),
);

function fixture(
  data,
  { status = 200, envelopeError, marketFetch, installedCards } = {},
) {
  const requests = [],
    toasts = [];
  const apiModule = { exports: {} };
  new Function("require", "exports", "fetch", apiSource)(
    (name) => {
      if (name === "vue") return vue;
      if (name === "./base-path.ts") return { apiURL: (path) => path };
      throw new Error("Unexpected API import " + name);
    },
    apiModule.exports,
    async (path, options) => {
      requests.push({ path, ...options });
      if (path === "/plugins/market" && marketFetch) return marketFetch();
      const reading = options.method === "GET";
      const result =
        path === "/plugins/organization"
          ? { revision: 0, folders: {}, order: [] }
          : path === "/plugins" && installedCards
            ? installedCards
            : [];
      return {
        ok: reading || status < 400,
        status: reading ? 200 : status,
        json: async () =>
          reading ? { data: result } : { data, error: envelopeError },
      };
    },
  );
  const module = { exports: {} };
  new Function("require", "exports", compiled)((name) => {
    if (name === "vue")
      return {
        ...vue,
        onMounted: () => {},
        onUnmounted: () => {},
        defineAsyncComponent: () => ({}),
      };
    if (name === "../api")
      return { ...apiModule.exports, toast: (text) => toasts.push(text) };
    if (name === "../features/plugin-organization")
      return { organizationGrid: () => [] };
    if (
      name.startsWith("vuetify/") ||
      name.endsWith(".vue") ||
      name === "vuedraggable"
    )
      return {};
    throw new Error("Unexpected component import " + name);
  }, module.exports);
  const scope = vue.effectScope();
  const state = scope.run(() =>
    module.exports.default.setup({}, { expose: () => {}, emit: () => {} }),
  );
  return { state, requests, toasts, stop: () => scope.stop() };
}
const plugin = {
  plugin_id: "OwnedFixture",
  instance_id: "OwnedFixture_clone",
  name: "测试插件",
};
const update = { generation: 7, revision: 9, target_version: "2.0" };

function cloneFixture(data, { status = 200, pending } = {}) {
  const source = { plugin_id: 'OwnedStandardFixture', instance_id: 'OwnedStandardFixture', name: '标准测试插件' };
  const requests = [], events = [];
  const apiModule = { exports: {} };
  new Function('require', 'exports', 'fetch', apiSource)(
    (name) => name === 'vue' ? vue : { apiURL: (p) => p }, apiModule.exports,
    async (path, options) => {
      requests.push({ path, ...options }); if (pending) await pending;
      return { ok: status < 400, status, json: async () => status < 400 ? { data } : { error: { message: '真实HTTP拒绝' } } };
    },
  );
  const helpers = { exports: {} };
  new Function('require', 'exports', transpile(readFileSync(new URL('../src/features/plugin-clone.ts', import.meta.url), 'utf8')))(() => ({ appBasePath: () => '' }), helpers.exports);
  const { descriptor: clone } = parse(readFileSync(new URL('../src/components/PluginCloneDialog.vue', import.meta.url), 'utf8'));
  const mod = { exports: {} };
  new Function('require', 'exports', transpile(compileScript(clone, { id: 'Clone' }).content))((name) => {
    if (name === 'vue') return vue;
    if (name === 'vuetify') return { useDisplay: () => ({ mdAndUp: vue.ref(true) }) };
    if (name === '../api') return apiModule.exports;
    if (name === '../features/plugin-clone') return helpers.exports;
    if (name.startsWith('vuetify/')) return {};
    throw new Error(name);
  }, mod.exports);
  const state = mod.exports.default.setup({ plugin: source }, { expose: () => {}, emit: (...a) => events.push(a) });
  state.form.suffix = 'QA1'; state.form.name = '我的分身草稿'; state.form.description = '保留内容';
  return { state, requests, events };
}
const cloneSuccess = { success: true, created: true, instance_id: 'OwnedStandardFixtureqa1', plugin_id: 'OwnedStandardFixture', source_instance_id: 'OwnedStandardFixture' };
for (const message of ['分身插件 OwnedStandardFixtureqa1 已存在', '']) {
  test('clone business-false preserves real error and draft: ' + (message || 'fallback'), async () => {
    const f = cloneFixture({ success: false, message }); await f.state.submit();
    assert.equal(f.state.error.value, message || '插件分身创建失败');
    assert.equal(f.state.form.suffix, 'QA1'); assert.equal(f.state.form.name, '我的分身草稿');
    assert.equal(f.state.form.description, '保留内容'); assert.equal(f.state.completed.value, false);
    assert.equal(f.state.busy.value, false); assert.deepEqual(f.events, []); assert.equal(f.requests.length, 1);
  });
}
test('business-false overrides even otherwise valid created identity', async () => {
  const f = cloneFixture({ ...cloneSuccess, success: false, message: '已有分身' }); await f.state.submit();
  assert.equal(f.state.error.value, '已有分身'); assert.deepEqual(f.events, []);
});
test('strict matching clone success emits once and sends original request identity', async () => {
  const f = cloneFixture(cloneSuccess); await f.state.submit(); await f.state.submit();
  assert.deepEqual(f.events, [['created', cloneSuccess]]); assert.equal(f.requests.length, 1);
  assert.equal(f.requests[0].path, '/plugins/OwnedStandardFixture/clone'); assert.equal(f.requests[0].method, 'POST');
  assert.equal(JSON.parse(f.requests[0].body).suffix, 'QA1'); assert.equal(f.state.completed.value, true);
});
for (const change of [{ created: false }, { instance_id: 'wrong' }, { plugin_id: 'wrong' }, { source_instance_id: 'wrong' }]) {
  test('clone malformed success still rejected: ' + JSON.stringify(change), async () => {
    const f = cloneFixture({ ...cloneSuccess, ...change }); await f.state.submit();
    assert.match(f.state.error.value, /无法确认/); assert.deepEqual(f.events, []); assert.equal(f.state.completed.value, false);
  });
}
test('clone HTTP error preserves draft and dialog', async () => {
  const f = cloneFixture(null, { status: 500 }); await f.state.submit();
  assert.equal(f.state.error.value, '真实HTTP拒绝'); assert.equal(f.state.form.suffix, 'QA1'); assert.deepEqual(f.events, []);
});
test('clone pending duplicate submit sends only one request', async () => {
  let release; const f = cloneFixture(cloneSuccess, { pending: new Promise((r) => release = r) });
  const request = f.state.submit(); await f.state.submit(); assert.equal(f.requests.length, 1);
  release(); await request; assert.deepEqual(f.events, [['created', cloneSuccess]]);
});
test('source market card is not filtered by an installed clone sharing plugin_id', async (t) => {
  const clone = { ...plugin, instance_id: 'OwnedFixtureqa1', source_instance_id: 'OwnedFixture', installed: true };
  const source = { ...plugin, instance_id: 'OwnedFixture', installed: false, description: '', author: '' };
  const f = fixture({}, { installedCards: [clone], marketFetch: async () => ({ ok: true, status: 200, json: async () => ({ data: [source] }) }) });
  t.after(f.stop); f.state.tab.value = 'market'; await vue.nextTick(); await f.state.load();
  assert.deepEqual(f.state.items.value, [clone]); assert.deepEqual(f.state.market.value, [source]);
  assert.deepEqual(f.state.visible.value, [source]);
});
