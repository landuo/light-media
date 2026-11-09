import './helpers/plugin-axios-fetch-adapter.mjs';
import * as sourceOrganization from '../src/features/plugin-organization.ts';
import * as pluginProject from '../src/features/plugin-project.ts';
import * as pluginMarket from '../src/features/plugin-market.ts';
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
          : path === "/plugins?state=installed" && installedCards
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
    if (name === "../features/plugin-market") return pluginMarket;
    if (name === '../features/plugin-project') return pluginProject;
    if (name === "../features/plugin-organization")
      return { ...sourceOrganization, organizationGrid: () => [] };
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

function configFixture(response, { status = 200, pending } = {}) {
  const { descriptor } = parse(
    readFileSync(
      new URL("../src/components/PluginConfigDialog.vue", import.meta.url),
      "utf8",
    ),
  );
  const requests = [],
    events = [],
    toasts = [];
  const realApi = { exports: {} };
  new Function("require", "exports", "fetch", apiSource)(
    (name) => (name === "vue" ? vue : { apiURL: (p) => p }),
    realApi.exports,
    async (path, options) => {
      requests.push({ path, ...options });
      if (pending) await pending;
      return {
        ok: status < 400,
        status,
        json: async () =>
          status < 400
            ? { data: response }
            : { error: { message: "fixture rejected config" } },
      };
    },
  );
  const mod = { exports: {} };
  new Function(
    "require",
    "exports",
    transpile(compileScript(descriptor, { id: "Config" }).content),
  )((name) => {
    if (name === "vue")
      return { ...vue, onMounted: () => {}, onUnmounted: () => {} };
    if (name === "vuetify")
      return { useDisplay: () => ({ mobile: vue.ref(false) }) };
    if (name === "../api")
      return { ...realApi.exports, toast: (s) => toasts.push(s) };
    if (name === "../features/plugin-forms")
      return { pluginFormIssues: () => [] };
    if (name === "../features/plugin-remote-api")
      return { createPluginRemoteAPI: () => ({}) };
    if (
      name === "../features/plugin-federation" ||
      name.startsWith("vuetify/") ||
      name.endsWith(".vue")
    )
      return {};
    throw new Error(name);
  }, mod.exports);
  const props = vue.reactive({ plugin: { ...plugin, available: true } });
  const state = mod.exports.default.setup(props, {
    expose: () => {},
    emit: (...a) => events.push(a),
  });
  state.loading.value = false;
  state.revision.value = 7;
  state.model.value = { enabled: true, note: "draft" };
  return { state, props, requests, events, toasts };
}
for (const response of [
  { success: false, message: "配置拒绝" },
  {},
  undefined,
]) {
  test(
    "standard config rejects non-success " + JSON.stringify(response),
    async () => {
      const f = configFixture(response);
      await f.state.save();
      assert.deepEqual(f.events, []);
      assert.deepEqual(f.toasts, []);
      assert.equal(
        f.state.error.value,
        response?.message || "插件配置保存失败",
      );
      assert.deepEqual(f.state.model.value, { enabled: true, note: "draft" });
      assert.equal(f.requests.length, 1);
      assert.equal(f.requests[0].method, "PUT");
    },
  );
}
test("standard config success is synchronous, no runs polling", async () => {
  const f = configFixture({ success: true });
  await f.state.save();
  assert.deepEqual(f.events, [["saved"]]);
  assert.deepEqual(f.toasts, ["测试插件配置已保存"]);
  assert.equal(f.requests.length, 1);
  assert.match(f.requests[0].path, /\/config$/);
  assert.deepEqual(JSON.parse(f.requests[0].body), {
    revision: 7,
    config: { enabled: true, note: "draft" },
  });
});
test("standard config HTTP failure preserves draft and no success", async () => {
  const f = configFixture(null, { status: 500 });
  await f.state.save();
  assert.equal(f.state.error.value, "fixture rejected config");
  assert.equal(f.requests.length, 1);
  assert.deepEqual(f.events, []);
  assert.deepEqual(f.toasts, []);
  assert.equal(f.state.model.value.note, "draft");
});
test("standard config duplicate save coalesces in-flight request", async () => {
  let done;
  const f = configFixture(
    { success: true },
    { pending: new Promise((r) => (done = r)) },
  );
  const first = f.state.save();
  await f.state.save();
  assert.equal(f.requests.length, 1);
  done();
  await first;
  assert.deepEqual(f.events, [["saved"]]);
});
test("standard config unavailable guard blocks local and remote save, keeps draft", async () => {
  const f = configFixture({ success: true });
  f.props.plugin.available = false;
  f.props.plugin.unavailable_reason = "等待标准适配";
  await f.state.save();
  await f.state.saveRemote({ enabled: false, note: "remote draft" });
  assert.deepEqual(f.requests, []);
  assert.deepEqual(f.events, []);
  assert.equal(f.state.model.value.note, "remote draft");
  assert.equal(f.state.error.value, "等待标准适配");
});
for (const action of ["runs", "restore-reviewed", "update"]) {
  test(
    "removed legacy action has no request or dialog: " + action,
    async (t) => {
      const f = fixture({});
      t.after(f.stop);
      await f.state.cardAction(action, {
        ...plugin,
        installed: true,
        update,
        has_update: true,
        restore_review: {},
      });
      assert.deepEqual(f.requests, []);
      assert.equal(f.state.confirmation.value, undefined);
    },
  );
}
for (const retained of [true, false]) {
  test(
    "formal unavailable uninstall retains backend semantics " + retained,
    async (t) => {
      const f = fixture({ success: true, data_retained: retained });
      t.after(f.stop);
      await f.state.cardAction("uninstall", { ...plugin, available: false });
      await f.state.confirmAction();
      assert.equal(f.requests[0].method, "DELETE");
      assert.match(f.toasts[0], retained ? /保留/ : /已清理/);
    },
  );
}
for (const action of [
  "install",
  "settings",
  "clone",
  "reset",
  "repository-update",
]) {
  test("unavailable standard action blocked: " + action, async (t) => {
    const f = fixture({});
    t.after(f.stop);
    await f.state.cardAction(action, {
      ...plugin,
      installed: true,
      available: false,
    });
    assert.deepEqual(f.requests, []);
    assert.equal(f.state.error.value, "插件暂不可用");
  });
}
for (const name of ["Plugins", "PluginManageCard", "PluginConfigDialog"]) {
  test(
    "standard " + name + " template compiles without old review/run UI",
    () => {
      const source = readFileSync(
        new URL("../src/components/" + name + ".vue", import.meta.url),
        "utf8",
      );
      const { descriptor } = parse(source);
      assert.deepEqual(
        compileTemplate({
          source: descriptor.template.content,
          filename: name + ".vue",
          id: name,
        }).errors,
        [],
      );
      assert.doesNotMatch(
        source,
        /\/runs|\/retry|\/abandon-unknown|\/restore-reviewed|update_review_pending/,
      );
    },
  );
}
