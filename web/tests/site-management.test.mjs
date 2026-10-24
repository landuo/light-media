import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as transfer from '../src/features/site-transfer.ts';
import * as resources from '../src/features/resources.ts';
import * as siteData from '../src/features/site-data.ts';

const fixtures = () => ({
  supported: [],
  sites: [
    {
      config: {
        id: 'one',
        kind: 'nexusphp',
        name: '示例一',
        base_url: 'https://one.invalid',
        enabled: true,
        pri: 1,
        ua: 'synthetic-browser',
        cookie: '',
        api_key: '',
        timeout: 25,
      },
      connection: { status: 'ok', duration_ms: 120 },
      credential_state: { cookie: true },
      stats: { uploaded: 1000, downloaded: 500 },
    },
    {
      config: {
        id: 'two',
        kind: 'nexusphp',
        name: '示例二',
        base_url: 'https://two.invalid',
        enabled: false,
        pri: 2,
        cookie: '',
        api_key: '',
        timeout: 25,
      },
      connection: { status: 'failed' },
    },
  ],
});
const singleResult = (status = 'created') => ({
  total: 1,
  imported: ['created', 'updated'].includes(status) ? 1 : 0,
  created: status === 'created' ? 1 : 0,
  updated: status === 'updated' ? 1 : 0,
  skipped: status === 'skipped' ? 1 : 0,
  failed: status === 'failed' ? 1 : 0,
  results: [
    {
      index: 1,
      name: '示例',
      status,
      message: status === 'failed' ? '读取失败' : '配置已保存，尚未测试连接',
      warnings: [],
    },
  ],
});
const compiled = new Map();
function sourceFor(name) {
  if (!compiled.has(name)) {
    const { descriptor } = parse(
      readFileSync(new URL('../src/components/' + name + '.vue', import.meta.url), 'utf8'),
    );
    const script = compileScript(descriptor, { id: name, inlineTemplate: true });
    compiled.set(
      name,
      ts.transpileModule(script.content, {
        compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
      }).outputText,
    );
  }
  return compiled.get(name);
}
async function mount(t, name, options = {}) {
  const fixture = { requests: [], events: [], toasts: [], downloads: 0, data: fixtures() },
    mounted = [],
    unmounted = [];
  const api = async (path, body, method) => {
    fixture.requests.push({
      path,
      body: body === undefined ? undefined : JSON.parse(JSON.stringify(body)),
      method,
    });
    if (options.api) {
      const result = await options.api(path, body, method, fixture);
      if (result !== undefined) return result;
    }
    if (path === '/sites/manage') return fixture.data;
    if (path === '/sites/priorities')
      return {
        ...fixture.data,
        sites: body.map((item) => fixture.data.sites.find((row) => row.config.id === item.id)),
      };
    if (path.endsWith('/refresh')) return fixture.data;
    if (path.endsWith('/history')) return { items: [] };
    if (method === 'DELETE') return { ...fixture.data, sites: [] };
    if (path === '/sites/import') return singleResult();
    if (path.endsWith('/resources'))
      return {
        candidates: [
          {
            resource: { site_id: 'one', id: 'resource', title: 'Synthetic', size: 100 },
            accepted: true,
          },
        ],
        sites: [],
        has_more: true,
        page: body.page,
      };
    throw new Error('Unexpected request ' + path);
  };
  const module = { exports: {} };
  new Function('require', 'exports', sourceFor(name))((dependency) => {
    if (/^vuetify\/components\//.test(dependency)) {
      const component = dependency.split('/').at(-1);
      if (component === 'VCard')
        return Object.fromEntries(
          ['VCard', 'VCardItem', 'VCardTitle', 'VCardText'].map((name) => [name, { name }]),
        );
      if (component === 'VGrid') return { VRow: { name: 'VRow' }, VCol: { name: 'VCol' } };
      return { [component]: { name: component } };
    }
    if (dependency === 'vuetify') return { useDisplay: () => ({ mdAndUp: vue.ref(true) }) };
    if (dependency === 'vue3-apexcharts') return { name: 'VueApexCharts' };
    if (dependency === '../features/site-data') return siteData;
    if (dependency === 'vue')
      return {
        ...vue,
        onMounted: (fn) => mounted.push(fn),
        onUnmounted: (fn) => unmounted.push(fn),
        withDirectives: (node) => node,
      };
    if (dependency === '../api')
      return {
        api,
        bytes: String,
        date: String,
        friendly: String,
        clone: (value) => JSON.parse(JSON.stringify(value)),
        errorText: (error) => error.message,
        toast: (text) => fixture.toasts.push(text),
        ui: { settings: { sites: [], downloaders: [] } },
        refreshSettings: async () => ({}),
      };
    if (dependency === '../features/site-transfer')
      return {
        ...transfer,
        downloadSiteExport: async () => {
          fixture.downloads++;
          if (options.exportError) throw new Error(options.exportError);
        },
      };
    if (dependency === '../features/resources') return resources;
    if (dependency.endsWith('.vue'))
      return { name: dependency.split('/').at(-1).replace('.vue', '') };
    throw new Error('Unexpected dependency ' + dependency);
  }, module.exports);
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup(options.props || {}, {
      expose: () => {},
      emit: (event, ...values) => fixture.events.push([event, ...values]),
    }),
  );
  let disposed = false;
  function unmount() {
    if (disposed) return;
    disposed = true;
    for (const callback of unmounted) callback();
    scope.stop();
  }
  t.after(unmount);
  for (const callback of mounted) await callback();
  await vue.nextTick();
  await vue.nextTick();
  function nodes() {
    const list = [];
    function visit(node) {
      if (node == null) return;
      if (Array.isArray(node)) return node.forEach(visit);
      list.push(node);
      if (typeof node === 'object') {
        if (node.children && !Array.isArray(node.children) && typeof node.children === 'object') {
          for (const [key, slot] of Object.entries(node.children))
            if (!key.startsWith('_') && typeof slot === 'function') visit(slot());
        } else visit(node.children);
      }
    }
    visit(render({}, []));
    return list;
  }
  const label = (value) => nodes().find((node) => node.props?.['aria-label'] === value);
  const button = (value) =>
    nodes().find(
      (node) =>
        node.type === 'button' &&
        typeof node.children === 'string' &&
        node.children.trim() === value,
    );
  return {
    fixture,
    unmount,
    nodes,
    label,
    button,
    text: () =>
      nodes()
        .filter((node) => typeof node === 'string')
        .join(' '),
    async click(value) {
      const node = label(value) || button(value);
      assert.ok(node, value);
      await node.props.onClick({ stopPropagation() {}, preventDefault() {} });
      await vue.nextTick();
    },
    async update(value, content) {
      label(value).props['onUpdate:modelValue'](content);
      await vue.nextTick();
    },
    async submit() {
      const form = nodes().find((node) => node.type === 'form');
      await form.props.onSubmit({ preventDefault() {} });
      await vue.nextTick();
    },
  };
}

const copy = (value) => JSON.parse(JSON.stringify(value));
async function settleConnectionTests() {
  for (let i = 0; i < 12; i++) await Promise.resolve();
  await vue.nextTick();
}
function nodeText(node) {
  if (node == null) return '';
  if (typeof node === 'string') return node;
  if (Array.isArray(node)) return node.map(nodeText).join(' ');
  return nodeText(node.children);
}
function connectionRun({ count = 5, reloadFails = false } = {}) {
  const ids = ['one', 'two', 'three', 'four', 'five'].slice(0, count);
  const names = ['示例一', '示例二', '示例三', '示例四', '示例五'];
  const server = {
    supported: [],
    sites: ids.map((id, index) => ({
      ...copy(fixtures().sites[0]),
      config: {
        ...copy(fixtures().sites[0].config),
        id,
        name: names[index],
        base_url: `https://${id}.invalid`,
        enabled: true,
        pri: index + 1,
      },
      connection: { status: 'failed', error: 'old failure' },
    })),
  };
  const stale = copy(server),
    pending = new Map();
  const run = { ids, started: [], active: 0, maximum: 0, loads: 0 };
  run.api = async (path, body) => {
    if (path === '/sites/manage' && !body) {
      run.loads++;
      if (run.loads > 1 && reloadFails) throw new Error('最终校准暂时不可用');
      return copy(server);
    }
    const match = path.match(/^\/sites\/manage\/([^/]+)\/refresh$/);
    if (!match) return;
    const id = decodeURIComponent(match[1]);
    run.started.push(id);
    run.maximum = Math.max(run.maximum, ++run.active);
    return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
  };
  run.complete = (id, { status = 'ok', uploaded = 2000, reject = false } = {}) => {
    const request = pending.get(id);
    assert.ok(request, `${id} must have a running connection test`);
    const row = server.sites.find((item) => item.config.id === id);
    row.connection = {
      status,
      duration_ms: 100,
      error: status === 'failed' ? 'synthetic failure' : '',
    };
    row.stats = { ...row.stats, uploaded };
    pending.delete(id);
    run.active--;
    if (reject) {
      request.reject(new Error('synthetic network failure'));
      return;
    }
    // Refresh responses contain whole lists; unrelated rows intentionally predate other results.
    const result = copy(stale);
    result.sites[result.sites.findIndex((item) => item.config.id === id)] = copy(row);
    request.resolve(result);
  };
  return run;
}

test('batch connection tests use all sites, cap concurrency at three, and continue after business and network failures', async (t) => {
  const run = connectionRun();
  const view = await mount(t, 'SiteManager', { api: run.api });
  view.label('全部测试').props.onClick();
  await settleConnectionTests();
  assert.deepEqual(run.started, ['one', 'two', 'three']);
  assert.equal(run.maximum, 3);
  assert.match(nodeText(view.label('示例一 资源')), /测试中/);
  assert.match(nodeText(view.label('示例四 资源')), /排队中/);
  assert.match(nodeText(view.label('站点连接测试进度')), /已完成 0 \/ 5/);

  run.complete('one');
  await settleConnectionTests();
  assert.ok(view.label('示例一 资源'), 'successful sites remain visible');
  assert.deepEqual(run.started, ['one', 'two', 'three', 'four']);
  run.complete('two', { status: 'failed' });
  await settleConnectionTests();
  assert.deepEqual(run.started, ['one', 'two', 'three', 'four', 'five']);
  run.complete('three', { status: 'failed', reject: true });
  run.complete('four');
  run.complete('five');
  await settleConnectionTests();

  const progress = nodeText(view.label('站点连接测试进度'));
  assert.match(progress, /已完成 5 \/ 5/);
  assert.match(progress, /成功 3/);
  assert.match(progress, /失败 2/);
  assert.equal(run.maximum, 3);
  assert.equal(run.loads, 2, 'one final list reload calibrates the finished batch');
  assert.equal(new Set(run.started).size, 5);
});

test('individual and batch connection tests share slots and deduplicate already running or queued sites', async (t) => {
  const run = connectionRun();
  const view = await mount(t, 'SiteManager', { api: run.api });
  const firstButton = view.label('测试 示例一');
  firstButton.props.onClick();
  await settleConnectionTests();
  assert.equal(view.label('测试 示例二').props.disabled, false, 'another site remains testable');
  view.label('测试 示例二').props.onClick();
  await settleConnectionTests();
  assert.deepEqual(run.started, ['one', 'two']);

  view.label('全部测试').props.onClick();
  firstButton.props.onClick();
  await settleConnectionTests();
  assert.deepEqual(run.started, ['one', 'two', 'three']);
  view.label('测试 示例四').props.onClick();
  await settleConnectionTests();
  assert.equal(run.started.length, 3, 'queued site cannot bypass the concurrency cap');
  run.complete('one');
  await settleConnectionTests();
  run.complete('two');
  await settleConnectionTests();
  assert.deepEqual(run.started, ['one', 'two', 'three', 'four', 'five']);
  run.complete('three');
  run.complete('four');
  run.complete('five');
  await settleConnectionTests();

  assert.equal(run.maximum, 3);
  assert.equal(run.active, 0);
  assert.equal(run.started.length, 5, 'each site is tested once across both entry points');
  assert.equal(new Set(run.started).size, 5);
});

test('out-of-order whole-list refresh responses preserve other site results when final calibration fails', async (t) => {
  const run = connectionRun({ count: 2, reloadFails: true });
  const view = await mount(t, 'SiteManager', { api: run.api });
  view.label('全部测试').props.onClick();
  await settleConnectionTests();
  run.complete('two', { uploaded: 22222 });
  await settleConnectionTests();
  assert.match(nodeText(view.label('示例二 资源')), /22222/);
  run.complete('one', { uploaded: 11111 });
  await settleConnectionTests();

  assert.match(nodeText(view.label('示例一 资源')), /11111/);
  assert.match(
    nodeText(view.label('示例二 资源')),
    /22222/,
    'later stale list must not undo another site result',
  );
  assert.match(nodeText(view.label('示例一 资源')), /连接正常/);
  assert.match(nodeText(view.label('示例二 资源')), /连接正常/);
  assert.equal(run.loads, 2);
  const progress = nodeText(view.label('站点连接测试进度'));
  assert.match(progress, /已完成 2 \/ 2/);
  assert.match(progress, /成功 2/);
  assert.match(progress, /失败 0/);
});

test('leaving site management cancels queued connection tests without sending new requests or reloading', async (t) => {
  const run = connectionRun();
  const view = await mount(t, 'SiteManager', { api: run.api });
  view.label('全部测试').props.onClick();
  await settleConnectionTests();
  assert.deepEqual(run.started, ['one', 'two', 'three']);
  view.unmount();
  run.complete('one');
  run.complete('two');
  run.complete('three');
  await settleConnectionTests();
  assert.deepEqual(
    run.started,
    ['one', 'two', 'three'],
    'unmounted queued sites are not contacted',
  );
  assert.equal(run.loads, 1, 'the detached page does not start a final reload');
  assert.equal(run.active, 0);
  assert.equal(view.fixture.toasts.length, 0);
});

test('site creation starts from a URL, with original credential tabs and effective UA/timeout/rate fields', async (t) => {
  const view = await mount(t, 'SiteManager');
  await view.click('添加站点');
  assert.ok(view.label('站点网址'));
  assert.ok(!view.text().includes('站点适配'));
  await view.update('站点网址', 'https://new-nexus.invalid');
  await view.update('User-Agent', 'test-UA');
  await view.update('超时时间', 40);
  await view.update('限制访问频率', true);
  await view.update('单位周期（秒）', 60);
  await view.update('周期内访问次数', 10);
  await view.update('访问间隔（秒）', 2);
  await view.submit();
  const body = view.fixture.requests.find(
    (request) => request.path === '/sites/manage' && request.body,
  ).body;
  assert.equal(body.base_url, 'https://new-nexus.invalid');
  assert.equal(body.kind, undefined);
  assert.equal(body.ua, 'test-UA');
  assert.equal(body.timeout, 40);
  assert.deepEqual([body.limit_interval, body.limit_count, body.limit_seconds], [60, 10, 2]);
});

test('editing preserves a valid zero priority and accepts priorities above one hundred', async (t) => {
  const view = await mount(t, 'SiteManager', {
    api: async (path, body, method, fixture) => {
      if (path === '/sites/manage' && !body) {
        fixture.data.sites[0].config.pri = 0;
        return fixture.data;
      }
    },
  });
  await view.click('示例一 更多操作');
  await view.click('编辑');
  assert.equal(view.label('优先级').props.min, '0');
  assert.equal(view.label('优先级').props.max, '10000');
  await view.submit();
  assert.equal(
    view.fixture.requests.find((request) => request.path === '/sites/manage' && request.body).body
      .pri,
    0,
  );
  await view.click('示例一 更多操作');
  await view.click('编辑');
  await view.update('优先级', 128);
  await view.submit();
  assert.equal(
    view.fixture.requests
      .filter((request) => request.path === '/sites/manage' && request.body)
      .at(-1).body.pri,
    128,
  );
});

test('inactive sites can be explicitly tested and cards open the correct resources without navigating away', async (t) => {
  const view = await mount(t, 'SiteManager');
  assert.equal(view.label('测试 示例二').props.disabled, false);
  await view.click('测试 示例二');
  assert.ok(view.fixture.requests.some((request) => request.path === '/sites/manage/two/refresh'));
  await view.click('示例一 资源');
  const child = view.nodes().find((node) => node.type?.name === 'SiteResourceDialog');
  assert.equal(child.props.site.id, 'one');
});

test('site deletion uses one in-page confirmation and no native confirm', async (t) => {
  const prior = globalThis.window;
  globalThis.window = {
    confirm: () => {
      throw new Error('Native confirm must not be used');
    },
  };
  t.after(() => {
    globalThis.window = prior;
  });
  const view = await mount(t, 'SiteManager');
  await view.click('示例一 更多操作');
  await view.click('删除');
  assert.ok(view.label('删除站点确认'));
  assert.equal(view.fixture.requests.filter((request) => request.method === 'DELETE').length, 0);
  await view.click('删除');
  assert.equal(view.fixture.requests.filter((request) => request.method === 'DELETE').length, 1);
});

test('sorting remains available in card actions and posts the complete order', async (t) => {
  const view = await mount(t, 'SiteManager');
  await view.click('示例一 更多操作');
  await view.click('排序');
  await view.click('下移 示例一');
  assert.deepEqual(
    view.fixture.requests.find((request) => request.path === '/sites/priorities').body,
    [
      { id: 'two', pri: 1 },
      { id: 'one', pri: 2 },
    ],
  );
  await view.click('退出');
  assert.equal(view.button('退出排序'), undefined);
  assert.equal(view.nodes().filter((node) => node.type === 'article').length, 2);
});

test('export downloads directly and failures remain visible without a success message', async (t) => {
  const view = await mount(t, 'SiteManager', { exportError: '凭据解密失败' });
  await view.click('导出站点');
  assert.equal(view.fixture.downloads, 1);
  assert.match(view.text(), /凭据解密失败/);
  assert.equal(view.fixture.toasts.length, 0);
});

test('import opens its original preview dialog and blocks old list writes throughout the import', async (t) => {
  const view = await mount(t, 'SiteManager');
  await view.click('示例一 更多操作');
  const oldToggle = view.button('停用');
  await view.click('导入站点');
  const child = view.nodes().find((node) => node.type?.name === 'SiteImportDialog');
  assert.ok(child);
  assert.equal(
    view.fixture.requests.filter((request) => request.path === '/sites/import').length,
    0,
  );
  child.props.onBusy(true);
  await oldToggle.props.onClick();
  assert.equal(view.fixture.requests.filter((request) => request.body).length, 0);
  await child.props.onImported(singleResult());
  child.props.onBusy(false);
  child.props.onClose();
  await vue.nextTick();
  assert.equal(
    view.fixture.requests.filter((request) => request.path === '/sites/manage').length,
    2,
  );
});

test('import file selection only previews safe metadata, then start submits each station with actual progress', async (t) => {
  let release, entered;
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  const hold = new Promise((resolve) => {
    release = resolve;
  });
  let number = 0;
  const view = await mount(t, 'SiteImportDialog', {
    api: async (path) => {
      if (path === '/sites/import') {
        number++;
        if (number === 1) {
          entered();
          await hold;
          return singleResult();
        }
        return singleResult('failed');
      }
    },
  });
  const rows = [
    {
      name: 'one',
      url: 'https://one.invalid/?passkey=synthetic-cookie',
      cookie: 'synthetic-cookie',
    },
    { name: 'two', url: 'https://two.invalid', cookie: 'synthetic-cookie' },
  ];
  const target = {
    files: [{ size: 100, text: async () => JSON.stringify(rows) }],
    value: 'sites.json',
  };
  await view.label('选择站点配置文件').props.onChange({ target });
  await vue.nextTick();
  assert.equal(target.value, '');
  assert.equal(view.fixture.requests.length, 0);
  assert.match(view.text(), /数据预览/);
  assert.ok(!view.text().includes('synthetic-cookie'));
  const running = view.click('开始导入');
  await started;
  assert.match(view.text(), /正在导入 0 \/ 2/);
  release();
  await running;
  assert.deepEqual(
    view.fixture.requests.map((request) => request.body.length),
    [1, 1],
  );
  assert.match(view.text(), /新增 1，更新 0，跳过 0，失败 1/);
  assert.deepEqual(
    view.fixture.events.filter(([event]) => event === 'busy').map(([, value]) => value),
    [true, false],
  );
  assert.equal(
    view.fixture.events.some(([event]) => event === 'close'),
    false,
    'partial failure stays visible',
  );
});

test('malformed site files show safe errors and keep the original file-selection stage usable', async (t) => {
  const view = await mount(t, 'SiteImportDialog');
  await view.label('选择站点配置文件').props.onChange({
    target: {
      files: [{ size: 20, text: async () => '{"cookie":"synthetic-cookie" invalid' }],
      value: 'sites.json',
    },
  });
  await vue.nextTick();
  assert.match(view.text(), /不是有效的 JSON/);
  assert.ok(!view.text().includes('synthetic-cookie'));
  assert.ok(view.label('选择站点配置文件'));
  assert.equal(view.fixture.requests.length, 0);
});

test('single-site resource browse loads recent items, searches and paginates using only its own site', async (t) => {
  const view = await mount(t, 'SiteResourceDialog', {
    props: { site: { id: 'one', name: '示例一', enabled: false } },
  });
  assert.deepEqual(view.fixture.requests[0], {
    path: '/sites/manage/one/resources',
    body: { query: '', page: 1 },
    method: undefined,
  });
  await view.update('站点资源关键词', 'Example');
  await view.submit();
  assert.deepEqual(view.fixture.requests.at(-1).body, { query: 'Example', page: 1 });
  await view.click('下一页');
  assert.deepEqual(view.fixture.requests.at(-1).body, { query: 'Example', page: 2 });
  const card = view.nodes().find((node) => node.type?.name === 'ResourceCard');
  card.props.onDownload(card.props.candidates[0]);
  await vue.nextTick();
  const download = view.nodes().find((node) => node.type?.name === 'AddDownloadDialog');
  assert.equal(download.props.candidate.resource.site_id, 'one');
  assert.deepEqual(download.props.request, { query: 'Example', page: 2, browse: true });
});

test('Rousi URL selects API credentials for add, edit and credential refresh', async (t) => {
  const view = await mount(t, 'SiteManager', {
    api: async (path, body, method, fixture) => {
      if (path === '/sites/manage' && !body) {
        Object.assign(fixture.data.sites[0].config, {
          kind: 'rousi',
          base_url: 'https://rousi.pro',
        });
        fixture.data.sites[0].credential_state = { api_key: true };
        return fixture.data;
      }
    },
  });
  await view.click('更新 示例一 凭据');
  assert.equal(view.label('API Key').props.type, 'password');
  await view.click('关闭站点编辑');
  await view.click('添加站点');
  await view.update('站点网址', 'https://www.rousi.pro');
  assert.ok(view.label('API Key'));
  await view.update('API Key', 'synthetic-passkey');
  await view.submit();
  assert.equal(view.fixture.requests.at(-1).body.api_key, 'synthetic-passkey');
});

test('site data dialog plots daily history and observed distribution; failed refresh keeps last success', async (t) => {
  const site = fixtures().sites[0];
  site.stats = {
    level: 'VIP',
    bonus: 8,
    uploaded: 3 * 1024 ** 3,
    downloaded: 1024 ** 3,
    seeding: 2,
    seeding_info: [
      [7, 1024 ** 3],
      [12, 2 * 1024 ** 3],
    ],
    captured_at: '2026-10-24T02:00:00Z',
  };
  let deny = false;
  const view = await mount(t, 'SiteUserDataDialog', {
    props: { site },
    api: async (path) => {
      if (path.endsWith('/history'))
        return {
          items: [
            { stats: site.stats },
            { stats: { uploaded: 1024 ** 3, downloaded: 0, captured_at: '2026-10-23T02:00:00Z' } },
          ],
        };
      if (path.endsWith('/refresh'))
        return {
          sites: [
            {
              ...site,
              connection: deny
                ? { status: 'failed', error: 'site_authentication_failed' }
                : { status: 'ok' },
            },
          ],
        };
    },
  });
  assert.match(view.text(), /总做种体积/);
  assert.match(view.text(), /\+2.00 GB/);
  const charts = () => view.nodes().filter((node) => node.type?.name === 'VueApexCharts');
  assert.deepEqual(
    charts()[0].props.series.map((s) => s.data),
    [
      [1, 3],
      [0, 1],
    ],
  );
  assert.deepEqual(charts()[1].props.series[0].data, [
    [7, 1],
    [12, 2],
  ]);
  await view.click('刷新站点数据');
  assert.equal(view.fixture.events[0][0], 'refreshed');
  deny = true;
  await view.click('刷新站点数据');
  assert.match(view.text(), /site_authentication_failed/);
  assert.match(view.text(), /显示上次成功数据/);
  assert.deepEqual(
    charts()[0].props.series.map((s) => s.data),
    [
      [1, 3],
      [0, 1],
    ],
  );
});

test('a closed site data dialog discards late refresh results', async (t) => {
  let finish;
  const site = fixtures().sites[0];
  const view = await mount(t, 'SiteUserDataDialog', {
    props: { site },
    api: async (path) => {
      if (path.endsWith('/history')) return { items: [] };
      if (path.endsWith('/refresh'))
        return new Promise((resolve) => {
          finish = resolve;
        });
    },
  });
  const pending = view.click('刷新站点数据');
  await Promise.resolve();
  view.unmount();
  finish({ sites: [site] });
  await pending;
  assert.equal(view.fixture.events.length, 0);
});

test('Rousi edit opens API credentials and submits its independent mailbox token without exposing saved values', async (t) => {
  const view = await mount(t, 'SiteManager', {
    api: async (path, body, method, fixture) => {
      if (path === '/sites/manage' && !body) {
        Object.assign(fixture.data.sites[0].config, {
          kind: 'rousi',
          base_url: 'https://rousi.pro',
          token: '',
        });
        fixture.data.sites[0].credential_state = { token: true };
        return fixture.data;
      }
    },
  });
  await view.click('示例一 更多操作');
  await view.click('编辑');
  const token = view.label('Authorization');
  assert.ok(token, 'API tab is available even when only the saved token exists');
  assert.ok(!token.props.disabled);
  assert.equal(token.props.type, 'password');
  assert.equal(token.props.placeholder, '已保存，保留原配置');
  assert.equal(view.label('API Key').props.placeholder, '填写站点 API Key');
  await view.update('Authorization', 'synthetic-new-mailbox-token');
  await view.update('API Key', 'synthetic-profile-key');
  await view.submit();
  const body = view.fixture.requests.find((r) => r.path === '/sites/manage' && r.body).body;
  assert.equal(body.token, 'synthetic-new-mailbox-token');
  assert.equal(body.api_key, 'synthetic-profile-key');
  assert.equal(body.cookie, '');
});

test('site toolbar has only all-site testing, cards use icons, and statistics renders real DOM content', async (t) => {
  const view = await mount(t, 'SiteManager');
  assert.equal(view.label('查找站点'), undefined);
  assert.equal(view.label('站点状态'), undefined);
  assert.equal(view.label('刷新'), undefined);
  assert.ok(view.label('全部测试'));
  const icons = view.nodes().filter((n) => n.type?.name === 'SiteIcon');
  assert.equal(icons.length, 2);
  assert.equal(icons[0].props.url, 'https://one.invalid');
  await view.click('站点统计');
  assert.match(view.text(), /累计上传/);
  assert.match(view.text(), /1 \/ 2 个站点提供数据/);
  assert.equal(
    view.nodes().some((n) => n.type === 'template'),
    false,
    'native template elements make dialog contents inert',
  );
});
