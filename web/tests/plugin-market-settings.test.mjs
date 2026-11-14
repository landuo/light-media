import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
const components = Object.fromEntries(
  ['PluginMarketSettingDialog', 'PluginSystemSettings'].map((name) => {
    const { descriptor } = parse(
      readFileSync(new URL('../src/components/' + name + '.vue', import.meta.url), 'utf8'),
    );
    const script = compileScript(descriptor, { id: name });
    const compiled = ts.transpileModule(script.content, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText;
    const template = ts.transpileModule(
      compileTemplate({
        source: descriptor.template.content,
        filename: name + '.vue',
        id: name,
        compilerOptions: { bindingMetadata: script.bindings },
      }).code,
      { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } },
    ).outputText;
    return [name, { compiled, template }];
  }),
);
const first = 'https://owned.invalid/a/index.json',
  second = 'https://owned.invalid/b/index.json';
function fixture({
  system = false,
  markets = [first],
  marketsLocked = false,
  localPaths = [],
  autoReload = false,
  serverURL = '',
  statisticShare = true,
  githubProxy = '', githubToken = '', repoGitHubToken = '', githubTokenSet = false, repoGitHubTokenSet = false,
  result = { success: true },
  wikiResult = { success: true, repos: [first, second], message: '原版 Python 仓库需 Go 适配' },
  readError,
  writeError,
  wait,
} = {}) {
  const { compiled, template } =
    components[system ? 'PluginSystemSettings' : 'PluginMarketSettingDialog'];
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
          if (path === '/plugins/markets/sync-wiki') return wikiResult;
          assert.equal(path, '/plugins/markets');
          if (!method) {
            if (readError) throw new Error(readError);
            return {
              plugin_markets: markets,
              plugin_markets_locked: marketsLocked,
              plugin_local_repo_paths: localPaths,
              plugin_auto_reload: autoReload,
              plugin_server_url: serverURL,
              plugin_statistic_share: statisticShare,
              github_proxy: githubProxy, github_token: githubToken, repo_github_token: repoGitHubToken, github_token_set: githubTokenSet, repo_github_token_set: repoGitHubTokenSet,
            };
          }
          if (wait) await wait;
          if (writeError) throw new Error(writeError);
          return result;
        },
        errorText: (e) => e.message,
        toast: (text) => toasts.push(text),
      };
    if (name.startsWith('vuetify/')) return new Proxy({}, { get: (_, name) => ({ name }) });
    if (name.endsWith('.vue') || name === 'vuedraggable') return { default: { name } };
    throw new Error('Unexpected import ' + name);
  }, module.exports);
  const state = module.exports.default.setup(
    {},
    { expose: () => {}, emit: (...args) => emitted.push(args) },
  );
  const render = { exports: {} };
  new Function('require', 'exports', template)(() => vue, render.exports);
  function nodes() {
    const nodes = [];
    function visit(node) {
      if (Array.isArray(node)) return node.forEach(visit);
      if (!node || typeof node !== 'object') return;
      nodes.push(node);
      if (node.children?.default) visit(node.children.default());
      else visit(node.children);
    }
    visit(render.exports.render(vue.proxyRefs(state), [], {}, vue.proxyRefs(state)));
    return nodes;
  }
  return { state, requests, emitted, toasts, nodes };
}
const systemFixture = (options) => fixture({ ...options, system: true });

test('all changed Vue templates compile, including market settings integration', () => {
  for (const name of [
    'PluginMarketSettingDialog',
    'PluginSystemSettings',
    'Plugins',
    'PluginManageCard',
  ]) {
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
test('environment-locked repositories reject every mutation and preserve exact sources without saving system settings', async () => {
  const sources = [second, first, second];
  const f = fixture({ markets: sources, marketsLocked: true });
  await f.state.load();
  assert.deepEqual(f.state.repos.value, sources);
  let controls = f.nodes();
  assert.equal(controls.find((node) => node.props?.class === 'repo-list').props.disabled, true);
  assert.equal(
    controls.find((node) => node.props?.['prepend-icon'] === 'mdi-cloud-sync-outline').props
      .disabled,
    true,
  );
  for (const label of [
    '本地插件仓库目录',
    '本地插件文件修改后自动重载',
    '插件统计与评分服务地址',
    '共享插件安装统计',
  ])
    assert.equal(
      controls.find((node) => node.props?.label === label),
      undefined,
      label,
    );
  assert.ok(
    controls.some(
      (node) =>
        typeof node.children === 'string' &&
        node.children.includes('仓库由启动环境设置，修改后需重启'),
    ),
  );
  f.state.newRepo.value = 'https://owned.invalid/new';
  f.state.addRepo();
  f.state.startEdit(0);
  f.state.editingUrl.value = 'https://owned.invalid/edited';
  f.state.saveEdit();
  f.state.removeRepo(0);
  f.state.switchMode('text');
  await f.state.syncWiki();
  assert.deepEqual(f.state.repos.value, sources);
  assert.equal(f.state.editingIndex.value, null);
  assert.equal(f.state.mode.value, 'list');
  assert.equal(f.requests.length, 1);
  // Even a programmatic draft replacement must never alter locked sources.
  f.state.repos.value = [];
  f.state.text.value = 'invalid';
  await f.state.save();
  assert.deepEqual(f.requests[1].body, {
    plugin_markets: sources,
  });
  assert.deepEqual(f.emitted, [['saved']]);
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
  f.nodes()
    .find((node) => node.props?.class === 'repo-list')
    .props['onUpdate:modelValue']([second, first]);
  assert.deepEqual(f.state.repos.value, [second, first]);
  f.state.startEdit(0);
  f.state.editingUrl.value = first;
  f.state.saveEdit();
  assert.equal(f.state.editingIndex.value, 0);
  assert.equal(f.state.saveDisabled.value, false);
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
test('empty list/text cannot save; unsubmitted row and add drafts are not mandatory save steps', async () => {
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
  f.state.startEdit(0);
  f.state.editingUrl.value = 'https://owned.invalid/uncommitted';
  assert.equal(f.state.saveDisabled.value, false);
  await f.state.save();
  assert.deepEqual(f.requests[1].body, { plugin_markets: [first] });
});
test('statistics sharing can be disabled independently before any repository or service is configured', async () => {
  const f = systemFixture({ markets: [] });
  await f.state.load();
  assert.equal(f.state.saveDisabled.value, false);
  f.state.statisticShare.value = false;
  assert.equal(f.state.saveDisabled.value, false);
  await f.state.save();
  assert.equal(f.requests[1].body.plugin_statistic_share, false);
  assert.equal(Object.hasOwn(f.requests[1].body, 'plugin_markets'), false);
  assert.deepEqual(f.emitted, [['saved']]);
  const off = systemFixture({ markets: [], statisticShare: false });
  await off.state.load();
  off.state.statisticShare.value = true;
  await off.state.save();
  assert.equal(off.requests[1].body.plugin_statistic_share, true);
});
test('successful PUT sends ordered sources without unrelated system settings and emits saved once', async () => {
  const f = fixture();
  await f.state.load();
  f.state.switchMode('text');
  f.state.text.value = second + ',' + first + ',' + second;
  await f.state.save();
  assert.deepEqual(f.requests[1], {
    path: '/plugins/markets',
    body: {
      plugin_markets: [second, first],
    },
    method: 'PUT',
  });
  assert.deepEqual(f.emitted, [['saved']]);
  assert.equal(f.toasts.length, 1);
});
test('Wiki sync writes through its own endpoint and updates draft without closing editor', async () => {
  const f = fixture();
  await f.state.load();
  await f.state.syncWiki();
  assert.deepEqual(f.requests[1], {
    path: '/plugins/markets/sync-wiki',
    body: {},
    method: undefined,
  });
  assert.deepEqual(f.state.repos.value, [first, second]);
  assert.equal(f.state.text.value, first + '\n' + second);
  assert.match(f.state.warning.value, /Go 适配/);
  assert.deepEqual(f.emitted, []);
  assert.equal(f.state.saving.value, false);
});
test('failed or malformed Wiki response preserves local editor and does not claim success', async () => {
  for (const wikiResult of [
    { success: false, message: '来源不可用' },
    { success: true, repos: ['file:///tmp/source'] },
  ]) {
    const f = fixture({ wikiResult });
    await f.state.load();
    await f.state.syncWiki();
    assert.deepEqual(f.state.repos.value, [first]);
    assert.notEqual(f.state.error.value, '');
    assert.deepEqual(f.toasts, []);
  }
});
test('local-only sources preserve source order/duplicates and explicitly opt into reload', async () => {
  const f = systemFixture({ markets: [], localPaths: ['/plugins/one'], autoReload: false });
  await f.state.load();
  assert.equal(f.state.localPaths.value, '/plugins/one');
  assert.equal(f.state.autoReload.value, false);
  assert.equal(f.state.saveDisabled.value, false);
  f.state.localPaths.value = ' /plugins/two,/plugins/one,/plugins/two ';
  f.state.autoReload.value = true;
  await f.state.save();
  assert.deepEqual(f.requests[1].body, {
    plugin_local_repo_paths: ['/plugins/two', '/plugins/one', '/plugins/two'],
    plugin_auto_reload: true,
    plugin_server_url: '',
    plugin_statistic_share: true,
  });
  assert.deepEqual(f.emitted, [['saved']]);
});
test('local source failures preserve source draft and automatic reload choice', async () => {
  const f = systemFixture({
    localPaths: ['/plugins/source'],
    autoReload: true,
    result: { success: false, message: '目录不存在' },
  });
  await f.state.load();
  f.state.localPaths.value = '/plugins/new';
  await f.state.save();
  assert.equal(f.state.localPaths.value, '/plugins/new');
  assert.equal(f.state.autoReload.value, true);
  assert.equal(f.state.error.value, '目录不存在');
  assert.deepEqual(f.emitted, []);
});
test('local-only configuration can remove its final trusted directory and disable reload', async () => {
  const f = systemFixture({ markets: [], localPaths: ['/plugins/source'], autoReload: true });
  await f.state.load();
  f.state.localPaths.value = '';
  f.state.autoReload.value = false;
  assert.equal(f.state.saveDisabled.value, false);
  await f.state.save();
  assert.deepEqual(f.requests[1].body, {
    plugin_local_repo_paths: [],
    plugin_auto_reload: false,
    plugin_server_url: '',
    plugin_statistic_share: true,
  });
});
test('configured statistics service and explicit disabled sharing persist independently of repositories', async () => {
  const f = systemFixture({
    markets: [],
    serverURL: 'https://owned.invalid/service',
    statisticShare: false,
  });
  await f.state.load();
  assert.equal(f.state.serverURL.value, 'https://owned.invalid/service');
  assert.equal(f.state.statisticShare.value, false);
  await f.state.save();
  assert.equal(f.requests[1].body.plugin_server_url, 'https://owned.invalid/service');
  assert.equal(f.requests[1].body.plugin_statistic_share, false);
  f.state.serverURL.value = '';
  assert.equal(f.state.saveDisabled.value, false);
  await f.state.save();
  assert.equal(f.requests[2].body.plugin_server_url, '');
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

test('system settings preserve repository authority and reject malformed unknown fields', async () => {
  const f = systemFixture({ markets: [second, first, second], marketsLocked: true });
  await f.state.load();
  f.state.autoReload.value = true;
  await f.state.save();
  assert.equal(Object.hasOwn(f.requests[1].body, 'plugin_markets'), false);
  for (const options of [
    { readError: '读取失败' },
    { localPaths: 'invalid' },
    { autoReload: 'true' },
  ]) {
    const bad = systemFixture(options);
    await bad.state.load();
    await bad.state.save();
    assert.equal(bad.state.saveDisabled.value, true);
    assert.equal(bad.requests.length, 1);
    assert.deepEqual(bad.emitted, []);
  }
});

test('fixed-source system fields are outside the repository dialog', async () => {
  const market = fixture();
  const system = systemFixture();
  await market.state.load();
  await system.state.load();
  // Fixed AccountSettingSystem.vue + zh-CN.ts labels, not generated from
  // the target component's implementation.
  for (const label of ['上报插件安装数据', '本地插件仓库路径', '插件热加载']) {
    assert.equal(
      market.nodes().some((node) => node.props?.label === label),
      false,
    );
    assert.equal(
      system.nodes().some((node) => node.props?.label === label),
      true,
    );
  }
  assert.equal(market.nodes().find((node) => node.props?.width === '56rem') !== undefined, true);
});


test('GitHub controls load only configured flags and untouched saves omit credential/network fields', async () => {
 const f=systemFixture({githubProxy:'https://owned-mirror.invalid/prefix',githubToken:'owned-default',repoGitHubToken:'owner/repo:owned-specific',githubTokenSet:true,repoGitHubTokenSet:true});await f.state.load();
 assert.equal(f.state.githubToken.value,'owned-default');assert.equal(f.state.repoGitHubToken.value,'owner/repo:owned-specific');
 assert.equal(f.state.githubTokenSet.value,true);assert.equal(f.state.repoGitHubTokenSet.value,true);
 const controls=f.nodes();
 assert.equal(controls.find(node=>node.props?.label==='Github Token').props.type,'text');
 assert.equal(controls.find(node=>node.props?.label==='指定仓库Github Token').props.type,'text');
 f.state.githubToken.value = null; f.state.repoGitHubToken.value = null;
 await f.state.save();
 for(const key of ['github_token','repo_github_token','github_proxy','clear_github_token','clear_repo_github_token'])assert.equal(Object.hasOwn(f.requests[1].body,key),false);
});
test('GitHub credentials and mirror save through host settings and clear only after actual success', async () => {
 const f=systemFixture();await f.state.load();
 f.state.githubProxy.value=' https://owned-mirror.invalid/prefix ';
 f.state.githubToken.value='owned-default';f.state.repoGitHubToken.value='owner/repo:owned-specific';
 await f.state.save();
 assert.equal(f.requests[1].body.github_proxy,'https://owned-mirror.invalid/prefix');
 assert.equal(f.requests[1].body.github_token,'owned-default');assert.equal(f.requests[1].body.repo_github_token,'owner/repo:owned-specific');
 assert.equal(f.state.githubToken.value,'owned-default');assert.equal(f.state.repoGitHubToken.value,'owner/repo:owned-specific');
 assert.equal(f.state.githubTokenSet.value,true);assert.equal(f.state.repoGitHubTokenSet.value,true);
 f.state.removeGitHubToken();f.state.removeRepoGitHubToken();await f.state.save();
 assert.equal(f.requests[2].body.clear_github_token,true);assert.equal(f.requests[2].body.clear_repo_github_token,true);
 assert.equal(f.state.githubTokenSet.value,false);assert.equal(f.state.repoGitHubTokenSet.value,false);
});
test('failed GitHub saves retain drafts; a newly typed token replaces a pending clear', async () => {
 const f=systemFixture({result:{success:false,message:'owned settings rejected'},githubTokenSet:true});await f.state.load();
 f.state.removeGitHubToken();f.state.githubToken.value='owned-replacement';f.state.inputGitHubToken('owned-replacement');
 f.state.repoGitHubToken.value='owner/repo:owned-specific';await f.state.save();
 assert.equal(f.requests[1].body.github_token,'owned-replacement');assert.equal(Object.hasOwn(f.requests[1].body,'clear_github_token'),false);
 assert.equal(f.state.githubToken.value,'owned-replacement');assert.equal(f.state.repoGitHubToken.value,'owner/repo:owned-specific');
 assert.equal(f.state.error.value,'owned settings rejected');assert.deepEqual(f.toasts,[]);
});

// Expectations come from the actual pinned path helper, not a duplicate parser.
test('local path fields preserve fixed source duplicates and only split English commas', async () => {
 const oracle=JSON.parse(readFileSync(new URL('../../testdata/standard-market/local-paths-oracle.json',import.meta.url),'utf8'));
 for (const row of oracle.cases.filter(row=>typeof row.value==='string' && row.value.startsWith('/'))) {
  const f=systemFixture({markets:[]});await f.state.load();f.state.localPaths.value=row.value;await f.state.save();
  assert.deepEqual(f.requests[1].body.plugin_local_repo_paths,row.paths);
 }
});
