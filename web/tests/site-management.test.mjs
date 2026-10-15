import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as transfer from '../src/features/site-transfer.ts';
import * as resources from '../src/features/resources.ts';

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
    mounted = [];
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
    if (dependency === 'vue')
      return {
        ...vue,
        onMounted: (fn) => mounted.push(fn),
        onUnmounted: () => {},
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
  t.after(() => scope.stop());
  for (const callback of mounted) await callback();
  await vue.nextTick();
  await vue.nextTick();
  function nodes() {
    const list = [];
    function visit(node) {
      if (node == null) return;
      if (Array.isArray(node)) return node.forEach(visit);
      list.push(node);
      if (typeof node === 'object') visit(node.children);
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

test('sorting posts the complete order and a filter exits sorting', async (t) => {
  const view = await mount(t, 'SiteManager');
  await view.click('排序');
  await view.click('下移 示例一');
  assert.deepEqual(
    view.fixture.requests.find((request) => request.path === '/sites/priorities').body,
    [
      { id: 'two', pri: 1 },
      { id: 'one', pri: 2 },
    ],
  );
  await view.update('站点状态', 'disabled');
  assert.equal(view.button('退出排序'), undefined);
  assert.equal(view.nodes().filter((node) => node.type === 'article').length, 1);
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
