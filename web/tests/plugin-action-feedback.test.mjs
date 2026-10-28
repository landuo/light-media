import './helpers/plugin-axios-fetch-adapter.mjs';
import * as sourceOrganization from '../src/features/plugin-organization.ts';
import * as pluginProject from '../src/features/plugin-project.ts';
import * as pluginMarket from '../src/features/plugin-market.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';

const transpile = (source) =>
  ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText;
const { descriptor } = parse(
  readFileSync(new URL('../src/components/Plugins.vue', import.meta.url), 'utf8'),
);
const compiled = transpile(compileScript(descriptor, { id: 'Plugins' }).content);
const apiSource = transpile(readFileSync(new URL('../src/api.ts', import.meta.url), 'utf8'));

function fixture(data, { status = 200, envelopeError, marketFetch, installedCards } = {}) {
  const requests = [],
    toasts = [];
  const apiModule = { exports: {} };
  new Function('require', 'exports', 'fetch', apiSource)(
    (name) => {
      if (name === 'vue') return vue;
      if (name === './base-path.ts') return { apiURL: (path) => path };
      throw new Error('Unexpected API import ' + name);
    },
    apiModule.exports,
    async (path, options) => {
      requests.push({ path, ...options });
      if (path.startsWith('/plugins/market?') && marketFetch) return marketFetch(path);
      const reading = options.method === 'GET';
      const result =
        path === '/plugins/organization' ? { revision: 0, folders: {}, order: [] } : path.startsWith('/plugins/market?') ? { items: [], sources: [] } : path === '/plugins?state=installed' && installedCards ? installedCards : [];
      return {
        ok: reading || status < 400,
        status: reading ? 200 : status,
        json: async () => (reading ? { data: result } : { data, error: envelopeError }),
      };
    },
  );
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === 'vue')
      return {
        ...vue,
        onMounted: () => {},
        onUnmounted: () => {},
        defineAsyncComponent: () => ({}),
      };
    if (name === '../api') return { ...apiModule.exports, toast: (text) => toasts.push(text) };
    if (name === '../features/plugin-market') return pluginMarket;
    if (name === '../features/plugin-project') return pluginProject;
    if (name === '../features/plugin-organization') return { ...sourceOrganization, organizationGrid: () => [] };
    if (name.startsWith('vuetify/') || name.endsWith('.vue') || name === 'vuedraggable') return {};
    throw new Error('Unexpected component import ' + name);
  }, module.exports);
  const scope = vue.effectScope();
  const state = scope.run(() =>
    module.exports.default.setup({}, { expose: () => {}, emit: () => {} }),
  );
  return { state, requests, toasts, stop: () => scope.stop() };
}
const plugin = {
  plugin_id: 'OwnedFixture',
  instance_id: 'OwnedFixture_clone',
  name: '测试插件',
};
const external = {
  ...plugin,
  instance_id: 'OwnedFixture',
  installed: true,
  repo_url: 'https://owned.invalid/a/index.json',
  repository_version: '2.0',
  has_update: true,
};

for (const action of ['install', 'repository-update']) {
  for (const message of ['制品摘要不匹配', '']) {
    test(`${action}: HTTP 200 business failure is shown without success or navigation`, async (t) => {
      const f = fixture({ success: false, message });
      t.after(f.stop);
      f.state.tab.value = 'market';
      await vue.nextTick();
      await new Promise(setImmediate);
      f.requests.length = 0;
      const choice = external;
      f.state.historyPlugin.value = choice;
      if (action === 'install') await f.state.install(plugin);
      else await f.state.updatePlugin(external);
      assert.equal(f.state.error.value, message || (action === 'install' ? '插件安装失败' : '插件更新失败'));
      assert.deepEqual(f.toasts, []);
      assert.equal(f.state.tab.value, 'market');
      assert.deepEqual(f.state.historyPlugin.value, choice);
      assert.equal(f.state.busy.value, false);
      assert.equal(f.requests.filter((r) => r.method === 'POST').length, 1);
      assert.equal(f.requests.filter((r) => r.method === 'GET').length, 0);
    });
  }
  test(`${action}: explicit success preserves standard artifact request identity`, async (t) => {
    const f = fixture({ success: true });
    t.after(f.stop);
    if (action === 'install') await f.state.install(plugin);
    else {
      f.state.historyPlugin.value = external;
      await f.state.updatePlugin(external);
      assert.equal(f.state.historyPlugin.value, undefined);
    }
    const request = f.requests.find((r) => r.method === 'POST');
    assert.equal(request.path, '/plugins/install');
    assert.deepEqual(JSON.parse(request.body), action === 'install'
      ? { plugin_id: 'OwnedFixture' }
      : { plugin_id: external.plugin_id, repo_url: external.repo_url, force: true });
    assert.equal(request.credentials, 'same-origin');
    assert.equal(f.toasts.length, 1);
    assert.equal(f.state.error.value, '');
    assert.equal(f.state.busy.value, false);
    assert.ok(f.requests.some((r) => r.path === '/plugins?state=installed' && r.method === 'GET'));
  });
  for (const data of [{}, { run: { id: 'legacy' } }, { review_pending: true }, { initialization: { id: 'legacy' } }]) {
    test(`${action}: unsupported legacy reply ${JSON.stringify(data)} never claims success`, async (t) => {
      const f = fixture(data);
      t.after(f.stop);
      if (action === 'install') await f.state.install(plugin);
      else {
        f.state.historyPlugin.value = external;
        await f.state.updatePlugin(external);
        assert.equal(f.state.historyPlugin.value.instance_id, external.instance_id);
      }
      assert.deepEqual(f.toasts, []);
      assert.equal(f.state.error.value, action === 'install' ? '插件安装失败' : '插件更新失败');
      assert.equal(f.requests.length, 1);
    });
  }
  test(`${action}: HTTP error remains visible and retry remains possible`, async (t) => {
    const f = fixture(undefined, { status: 500, envelopeError: { code: 'owned_failure', message: '服务失败' } });
    t.after(f.stop);
    f.state.historyPlugin.value = external;
    if (action === 'install') await f.state.install(plugin);
    else await f.state.updatePlugin(external);
    assert.equal(f.state.error.value, '服务失败');
    assert.equal(f.state.busy.value, false);
    assert.deepEqual(f.toasts, []);
  });
}

test('external install sends the exact selected source without force or fabricated version', async (t) => {
  const f = fixture({ success: true });
  t.after(f.stop);
  await f.state.install(external);
  const request = f.requests.find((r) => r.method === 'POST');
  assert.equal(request.path, '/plugins/install');
  assert.deepEqual(JSON.parse(request.body), {
    plugin_id: external.plugin_id,
    repo_url: external.repo_url,
  });
});
for (const success of [true, false]) {
  test(`external update uses install contract, success=${success}`, async (t) => {
    const f = fixture({ success, message: '获取失败' });
    t.after(f.stop);
    await f.state.cardAction('repository-update', external);
    assert.deepEqual(f.state.historyPlugin.value, external);
    assert.equal(f.state.historyShowUpdate.value, true);
    assert.equal(f.state.confirmation.value, undefined);
    await f.state.updatePlugin(external);
    const request = f.requests.find((r) => r.method === 'POST');
    assert.equal(request.path, '/plugins/install');
    assert.deepEqual(JSON.parse(request.body), {
      plugin_id: external.plugin_id,
      repo_url: external.repo_url,
      force: true,
    });
    assert.equal(f.toasts.length, success ? 1 : 0);
    assert.equal(f.state.error.value, success ? '' : '获取失败');
    assert.equal(f.state.historyPlugin.value === undefined, success);
    assert.equal(f.state.busy.value, false);
  });
}
test('clone without an exact market candidate or missing bound source/version does not get an update action', async (t) => {
  for (const card of [
    { ...external, source_instance_id: 'source', has_update: false },
    { ...external, repo_url: '' },
    { ...external, repository_version: '' },
  ]) {
    const f = fixture({ success: true });
    t.after(f.stop);
    await f.state.cardAction('repository-update', card);
    assert.equal(f.state.confirmation.value, undefined);
    assert.equal(f.state.historyPlugin.value, undefined);
    await f.state.updatePlugin(card);
    assert.equal(f.requests.length, 0);
    assert.equal(f.state.busy.value, false);
  }
});
test('exact clone update candidate uses clone instance identity and never overwrites its source', async (t) => {
  const f = fixture({ success: true }); t.after(f.stop);
  const clone = { ...external, instance_id: 'OwnedFixture_clone', plugin_id: 'OwnedFixture', source_instance_id: 'OwnedFixture', repo_url: 'https://owned.invalid/clones/index.json' };
  await f.state.cardAction('repository-update', clone);
  assert.equal(f.state.historyPlugin.value.instance_id, clone.instance_id);
  await f.state.updatePlugin(clone);
  const request = f.requests.find(request => request.method === 'POST');
  assert.equal(JSON.parse(request.body).plugin_id, 'OwnedFixture_clone');
  assert.equal(JSON.parse(request.body).repo_url, clone.repo_url);
  assert.equal(f.toasts[0], clone.name + '已更新');
});
const marketCandidate = { ...external, installed: false, has_update: false };
test('source settings saved clears stale market and reloads existing lists', async (t) => {
  const f = fixture({ success: true });
  t.after(f.stop);
  f.state.tab.value = 'market';
  await new Promise(setImmediate);
  f.requests.length = 0;
  f.state.marketSettings.value = true;
  f.state.market.value = [marketCandidate];
  await f.state.marketSettingsSaved();
  assert.equal(f.state.marketSettings.value, false);
  assert.deepEqual(f.state.market.value, []);
  assert.ok(f.requests.some((r) => r.path === '/plugins/market?details=true&force=true'));
});
test('old outstanding discovery cannot repopulate removed sources after settings save', async (t) => {
  let finishOld,
    calls = 0;
  const old = new Promise((resolve) => {
    finishOld = resolve;
  });
  const response = (items) => ({ ok: true, status: 200, json: async () => ({ data: { items, sources: [] } }) });
  const f = fixture(
    { success: true },
    { marketFetch: () => (++calls === 1 ? old : Promise.resolve(response([]))) },
  );
  t.after(f.stop);
  f.state.tab.value = 'market';
  await new Promise(setImmediate);
  assert.equal(calls, 1);
  const refresh = f.state.marketSettingsSaved();
  finishOld(response([marketCandidate]));
  await refresh;
  await new Promise(setImmediate);
  assert.deepEqual(f.state.market.value, []);
});
test('marketplace refresh follows entry and explicit actions without a periodic status timer', async (t) => {
  let finish,
    calls = 0;
  const pending = new Promise((resolve) => {
    finish = resolve;
  });
  const f = fixture(
    { success: true },
    {
      marketFetch: () => {
        calls++;
        return pending;
      },
    },
  );
  t.after(f.stop);
  f.state.tab.value = 'market';
  await new Promise(setImmediate);
  assert.equal(compiled.includes("setInterval("), false);
  assert.equal(calls, 1);
  finish({ ok: true, status: 200, json: async () => ({ data: { items: [marketCandidate], sources: [] } }) });
  await new Promise(setImmediate);
  assert.deepEqual(f.state.market.value, [marketCandidate]);
  assert.equal(calls, 1);
  await new Promise(setImmediate);
  assert.equal(calls, 1);
});
test('repeated explicit refresh during slow discovery coalesces into one fresh read', async (t) => {
  let finish,
    calls = 0;
  const pending = new Promise((resolve) => {
    finish = resolve;
  });
  const response = (items) => ({ ok: true, status: 200, json: async () => ({ data: { items, sources: [] } }) });
  const f = fixture(
    { success: true },
    { marketFetch: () => (++calls === 1 ? pending : Promise.resolve(response([]))) },
  );
  t.after(f.stop);
  f.state.tab.value = 'market';
  await new Promise(setImmediate);
  const refreshes = Array.from({ length: 10 }, () => f.state.load(true));
  assert.equal(calls, 1);
  finish(response([marketCandidate]));
  await Promise.all(refreshes);
  assert.equal(calls, 2);
  assert.deepEqual(f.state.market.value, []);
  assert.equal(f.requests.filter((r) => r.path.includes('force=true')).length, 1);
});

test('partial repository failure stays visible with usable cards and explicit refresh retries', async (t) => {
  let retried = false;
  const f = fixture({ success: true }, { marketFetch: (path) => {
    retried ||= path.includes('force=true');
    return { ok: true, status: 200, json: async () => ({ data: {
      items: [marketCandidate],
      sources: retried
        ? [{ repo_url: 'https://owned.invalid/index', success: true }]
        : [{ repo_url: 'https://owned.invalid/index', success: false, message: '仓库索引读取失败' }],
    } }) };
  } });
  t.after(f.stop);
  f.state.tab.value = 'market';
  await new Promise(setImmediate);
  assert.deepEqual(f.state.market.value, [marketCandidate]);
  assert.equal(f.state.marketFailures.value.length, 1);
  assert.equal(f.state.error.value, '');
  await f.state.load(true);
  assert.equal(retried, true);
  assert.deepEqual(f.state.marketFailures.value, []);
  assert.deepEqual(f.state.market.value, [marketCandidate]);
});

test('malformed detailed market response keeps existing results and reports failure', async (t) => {
  const f = fixture({ success: true }, { marketFetch: () => ({ ok: true, status: 200, json: async () => ({ data: [] }) }) });
  t.after(f.stop);
  f.state.market.value = [marketCandidate];
  f.state.tab.value = 'market';
  await new Promise(setImmediate);
  assert.deepEqual(f.state.market.value, [marketCandidate]);
  assert.equal(f.state.error.value, '插件市场响应无效');
});

test('market refresh transfers upgrade source to installed cards and excludes installed upgrades from market grid', async (t) => {
  const existing = { ...external, repo_url: 'https://owned.invalid/old', repository_version: '1.0', has_update: false };
  const f = fixture({ success: true }, {
    installedCards: [existing],
    marketFetch: () => ({ ok: true, status: 200, json: async () => ({ data: { items: [external, { ...marketCandidate, instance_id: 'Other', plugin_id: 'Other' }], sources: [] } }) }),
  });
  t.after(f.stop);
  f.state.tab.value = 'market';
  await new Promise(setImmediate);
  assert.equal(f.state.items.value[0].repo_url, external.repo_url);
  assert.equal(f.state.items.value[0].repository_version, '2.0');
  assert.equal(f.state.items.value[0].has_update, true);
  assert.equal(f.state.market.value.length, 1);
  assert.equal(f.state.market.value[0].instance_id, 'Other');
});

for (const dataRetained of [true, false, undefined]) {
  test(`uninstall uses formal DELETE and backend retention=${dataRetained}, not card identity`, async (t) => {
    const f = fixture({
      success: true,
      uninstalled: true,
      ...(dataRetained === undefined ? {} : { data_retained: dataRetained }),
    });
    t.after(f.stop);
    // Deliberately misleading card identity: server state, not suffix or metadata,
    // is authoritative about whether a loaded clone's data was cleared.
    const card = {
      ...plugin,
      instance_id: 'ordinary/id',
      source_instance_id: dataRetained ? 'clone-source' : undefined,
    };
    f.state.confirmation.value = { action: 'uninstall', plugin: card };
    await f.state.confirmAction();
    const request = f.requests.find((r) => r.method === 'DELETE');
    assert.equal(request.path, '/plugins/ordinary%2Fid');
    assert.equal(request.body, undefined);
    assert.equal(request.credentials, 'same-origin');
    assert.equal(f.requests.filter((r) => r.method === 'POST').length, 0);
    assert.deepEqual(f.toasts, [
      dataRetained === true
        ? '插件已卸载，配置及数据保留'
        : dataRetained === false
          ? '插件已卸载，配置及数据已清理'
          : '插件已卸载',
    ]);
    assert.equal(f.state.confirmation.value, undefined);
    assert.equal(f.state.busy.value, false);
    assert.ok(f.requests.some((r) => r.path === '/plugins?state=installed' && r.method === 'GET'));
  });
}
for (const action of ['reset', 'uninstall']) {
  for (const failure of ['business-message', 'business-fallback', 'http']) {
    test(`${action}: ${failure} preserves confirmation and never announces success`, async (t) => {
      const message = failure === 'business-fallback' ? '' : '服务拒绝操作';
      const f = fixture(
        { success: false, message, data_retained: false },
        failure === 'http'
          ? { status: 500, envelopeError: { code: 'owned_failure', message } }
          : {},
      );
      t.after(f.stop);
      const choice = { action, plugin };
      f.state.confirmation.value = choice;
      await f.state.confirmAction();
      assert.deepEqual(f.state.confirmation.value, choice);
      assert.deepEqual(f.toasts, []);
      assert.equal(f.state.busy.value, false);
      assert.equal(
        f.state.error.value,
        message || (action === 'reset' ? '插件重置失败' : '插件卸载失败'),
      );
      assert.equal(f.requests.length, 1);
      assert.equal(f.requests[0].method, action === 'uninstall' ? 'DELETE' : 'POST');
      assert.equal(
        f.requests[0].path,
        '/plugins/OwnedFixture_clone' + (action === 'reset' ? '/reset' : ''),
      );
    });
  }
}
test('reset retains its existing POST contract and clears confirmation only on success', async (t) => {
  const f = fixture({ success: true });
  t.after(f.stop);
  f.state.confirmation.value = { action: 'reset', plugin };
  await f.state.confirmAction();
  assert.equal(f.requests[0].method, 'POST');
  assert.equal(f.requests[0].path, '/plugins/OwnedFixture_clone/reset');
  assert.deepEqual(JSON.parse(f.requests[0].body), {});
  assert.deepEqual(f.toasts, ['插件配置及数据已重置']);
  assert.equal(f.state.confirmation.value, undefined);
});

test('confirmation template retains the open dialog with an inline error alert', () => {
  const compiledTemplate = compileTemplate({
    source: descriptor.template.content,
    filename: 'Plugins.vue',
    id: 'Plugins',
  });
  assert.deepEqual(compiledTemplate.errors, []);
  const confirmationSection = descriptor.template.content.slice(
    descriptor.template.content.indexOf(':model-value="!!confirmation"'),
    descriptor.template.content.indexOf(':model-value="history !== null"'),
  );
  assert.match(confirmationSection, /<p v-if="error" role="alert" class="error">/);
  assert.doesNotMatch(confirmationSection, /配置及运行数据会保留/);
});

for (const action of ['open', 'settings', 'install', 'clone', 'reset', 'repository-update']) {
  test(`unavailable card rejects ${action} without HTTP or dialogs`, async (t) => {
    const f = fixture({ success: true }); t.after(f.stop);
    await f.state.cardAction(action, { ...plugin, installed: true, available: false, unavailable_reason: '插件尚未适配新的标准模块' });
    assert.equal(f.state.error.value, '插件尚未适配新的标准模块');
    assert.deepEqual(f.requests, []);
    assert.equal(f.state.selected.value, undefined);
    assert.equal(f.state.cloneSource.value, undefined);
    assert.equal(f.state.confirmation.value, undefined);
    assert.deepEqual(f.toasts, []);
  });
}
for (const action of ['install', 'openPlugin']) {
  test(`unavailable direct ${action} rejects bypass with fallback reason`, async (t) => {
    const f = fixture({ success: true }); t.after(f.stop);
    await f.state[action]({ ...plugin, available: false });
    assert.equal(f.state.error.value, '插件暂不可用');
    assert.deepEqual(f.requests, []);
  });
}
test('retired update and restore actions cannot revive the removed executor', async (t) => {
  const f = fixture({ success: true });
  t.after(f.stop);
  for (const action of ['update', 'restore-reviewed']) await f.state.cardAction(action, external);
  assert.equal(f.state.restoreReviewed, undefined);
  assert.equal(f.state.confirmation.value, undefined);
  assert.deepEqual(f.requests, []);
  assert.deepEqual(f.toasts, []);
});
for (const action of ['reset', 'repository-update']) {
  test(`unavailable ${action} rejects writes and retains the active dialog`, async (t) => {
    const f = fixture({ success: true }); t.after(f.stop);
    const choice = { action, plugin: { ...plugin, available: false } };
    if (action === 'reset') {
      f.state.confirmation.value = choice;
      await f.state.confirmAction();
      assert.deepEqual(f.state.confirmation.value, choice);
    } else {
      f.state.historyPlugin.value = choice.plugin;
      await f.state.updatePlugin(choice.plugin);
      assert.deepEqual(f.state.historyPlugin.value, choice.plugin);
    }
    assert.deepEqual(f.requests, []);
    assert.equal(f.state.error.value, '插件暂不可用');
  });
}
test('unavailable uninstall remains formal DELETE with real retained feedback', async (t) => {
  const f = fixture({ success: true, data_retained: true }); t.after(f.stop);
  await f.state.cardAction('uninstall', { ...plugin, available: false });
  await f.state.confirmAction();
  assert.equal(f.requests[0].method, 'DELETE');
  assert.equal(f.toasts[0], '插件已卸载，配置及数据保留');
});
for (const available of [true, undefined]) {
  test(`available=${available} keeps settings and install compatibility`, async (t) => {
    const f = fixture({ success: true }); t.after(f.stop);
    const card = { ...plugin, installed: true, available };
    await f.state.cardAction('settings', card);
    assert.deepEqual(f.state.selected.value, card);
    await f.state.install({ ...card, installed: false });
    assert.equal(f.requests[0].method, 'POST');
    assert.equal(f.toasts[0], '测试插件已安装');
  });
}

test('config draft survives availability change; local and remote saves do not send HTTP', async () => {
  const { descriptor: config } = parse(readFileSync(new URL('../src/components/PluginConfigDialog.vue', import.meta.url), 'utf8'));
  const code = transpile(compileScript(config, { id: 'Config' }).content);
  const requests = [], events = [];
  const mod = { exports: {} };
  new Function('require', 'exports', code)((name) => {
    if (name === 'vue') return { ...vue, onMounted: () => {}, onUnmounted: () => {} };
    if (name === 'vuetify') return { useDisplay: () => ({ mobile: vue.ref(false) }) };
    if (name === '../api') return { api: (...args) => { requests.push(args); }, errorText: String, toast: () => {} };
    if (name === '../features/plugin-forms') return { pluginFormIssues: () => [] };
    if (name === '../features/plugin-runtime-support') return { pluginRuntimeSupportNotice: () => '' };
    if (name === '../features/plugin-remote-api') return { createPluginRemoteAPI: () => ({}) };
    if (name === '../features/plugin-federation') return {};
    if (name.startsWith('vuetify/') || name.endsWith('.vue')) return {};
    throw new Error('Unexpected import ' + name);
  }, mod.exports);
  const props = vue.reactive({ plugin: { ...plugin, available: true } });
  const state = mod.exports.default.setup(props, { expose: () => {}, emit: (...args) => events.push(args) });
  state.loading.value = false;
  state.model.value = { enabled: true, draft: '保留编辑' };
  props.plugin = { ...props.plugin, available: false, unavailable_reason: '插件尚未适配新的标准模块' };
  await state.save();
  assert.deepEqual(state.model.value, { enabled: true, draft: '保留编辑' });
  await state.saveRemote({ enabled: true, draft: '远程编辑' });
  assert.deepEqual(state.model.value, { enabled: true, draft: '远程编辑' });
  assert.equal(state.error.value, '插件尚未适配新的标准模块');
  assert.deepEqual(requests, []); assert.deepEqual(events, []);
});

test('unavailable card template disables mutations but keeps source link and uninstall', () => {
  const { descriptor: card } = parse(readFileSync(new URL('../src/components/PluginManageCard.vue', import.meta.url), 'utf8'));
  const result = compileTemplate({ source: card.template.content, filename: 'PluginManageCard.vue', id: 'Card' });
  assert.deepEqual(result.errors, []);
  const nodes = [];
  const visit = (node) => { if (node.type === 1) nodes.push(node); for (const child of node.children || []) visit(child); };
  visit(parse(readFileSync(new URL('../src/components/PluginManageCard.vue', import.meta.url), 'utf8')).descriptor.template.ast);
  const attr = (n, name) => n.props.find((p) => p.type === 6 && p.name === name)?.value?.content;
  const disabled = (n) => n.props.find((p) => p.type === 7 && p.name === 'bind' && p.arg?.content === 'disabled')?.exp?.content;
  for (const title of ['设置', '重置', '分身']) {
    assert.match(disabled(nodes.find((n) => attr(n, 'title') === title)), /plugin.available === false/);
  }
  const updateNode = nodes.find((n) => n.props.some((p) => p.type === 7 && p.name === 'on' && p.exp?.content.includes("'repository-update'")));
  assert.match(disabled(updateNode), /plugin.available === false/);
  assert.equal(disabled(nodes.find((n) => attr(n, 'title') === '卸载')), undefined);
  assert.ok(nodes.some((n) => n.tag === 'a' && n.props.some((p) => p.arg?.content === 'href' && p.exp?.content === 'plugin.author_url')));
  assert.match(card.template.content, /plugin.unavailable_reason/);
});

 test('installed refresh propagates unavailable status to an already open config dialog', async (t) => {
  const current = { ...plugin, installed: true, available: false, unavailable_reason: '等待适配' };
  const f = fixture({}, { installedCards: [current] }); t.after(f.stop);
  f.state.selected.value = { ...plugin, installed: true, available: true };
  await f.state.load();
  assert.deepEqual(f.state.selected.value, current);
  assert.equal(f.state.selected.value.instance_id, plugin.instance_id);
});

test('configuration template with unavailable notice and disabled save compiles', () => {
  const { descriptor: config } = parse(readFileSync(new URL('../src/components/PluginConfigDialog.vue', import.meta.url), 'utf8'));
  assert.deepEqual(compileTemplate({ source: config.template.content, filename: 'PluginConfigDialog.vue', id: 'Config' }).errors, []);
  assert.match(config.template.content, /:disabled="\s*plugin.available === false/);
});

test('market displays twenty then appends twenty and resets pagination on filter changes', async (t) => {
  const f = fixture({ success: true }); t.after(f.stop);
  f.state.market.value = Array.from({ length: 45 }, (_, i) => ({ ...plugin, instance_id: 'P' + i, name: 'Plugin ' + i, description: 'test', author: 'owned', plugin_id: 'P' + i }));
  assert.equal(f.state.visible.value.length, 20);
  const statuses = [];
  f.state.loadMoreMarket({ done: value => statuses.push(value) });
  assert.equal(f.state.visible.value.length, 40);
  f.state.loadMoreMarket({ done: value => statuses.push(value) });
  assert.equal(f.state.visible.value.length, 45);
  assert.deepEqual(statuses, ['ok', 'ok']);
  f.state.loadMoreMarket({ done: value => statuses.push(value) });
  assert.deepEqual(statuses, ['ok', 'ok', 'empty'], 'fixed loadMarketMore reports empty only for an empty batch');
  f.state.market.value = [...f.state.market.value]; await vue.nextTick();
  assert.equal(f.state.visible.value.length, 20, 'fixed marketList replacement resets first twenty');
  f.state.loadMoreMarket({ done: () => {} });
  f.state.marketStatistics.value = { P1: 1 }; await vue.nextTick();
  assert.equal(f.state.visible.value.length, 20, 'fixed statistics watcher resets first twenty');
  f.state.query.value = 'Plugin 1'; await vue.nextTick();
  assert.equal(f.state.marketLimit.value, 20);
  assert.equal(f.state.visible.value.length, 11);
});

test('global search loads market once and clicking an uninstalled result executes the actual installer', async (t) => {
  const candidate = { ...plugin, plugin_id: 'Other', instance_id: 'Other', name: 'Other', description: 'owned', installed: false, available: true, repo_url: 'https://owned.invalid/index' };
  const f = fixture({ success: false, message: 'owned install refused' }, { marketFetch: async () => ({ ok: true, status: 200, json: async () => ({ data: { items: [candidate], sources: [] } }) }) }); t.after(f.stop);
  f.state.items.value = [{ ...plugin, installed: true }];
  await f.state.openSearch();
  assert.deepEqual(f.state.searchPlugins.value.map(card => card.instance_id), [plugin.instance_id, 'Other']);
  await f.state.searchSelected(candidate);
  assert.equal(f.state.searchOpen.value, false);
  assert.equal(f.state.error.value, 'owned install refused');
  assert.deepEqual(f.toasts, []);
  const write = f.requests.find(request => request.method === 'POST');
  assert.equal(write.path, '/plugins/install');
  assert.deepEqual(JSON.parse(write.body), { plugin_id: 'Other', repo_url: candidate.repo_url });
});

test('fixed market sort selection and Enter close the filter menu, IME composition retains it', async (t) => {
  const f = fixture({ success: true }); t.after(f.stop);
  f.state.marketFilterMenu.value = true;
  f.state.selectMarketSort('name');
  assert.equal(f.state.marketSort.value, 'name');
  assert.equal(f.state.marketFilterMenu.value, false);
  f.state.marketFilterMenu.value = true;
  f.state.submitMarketFilter({ isComposing: true });
  assert.equal(f.state.marketFilterMenu.value, true);
  f.state.submitMarketFilter({ isComposing: false });
  assert.equal(f.state.marketFilterMenu.value, false);
});
