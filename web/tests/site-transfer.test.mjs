import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { parse, compileScript } from '@vue/compiler-sfc';
import ts from 'typescript';
import * as vue from 'vue';
import * as transfer from '../src/features/site-transfer.ts';

const moviePilot = [
  {
    name: '馒头',
    domain: 'm-team.cc',
    url: 'https://api.m-team.cc',
    apikey: 'synthetic-credential',
    is_active: true,
  },
];
const localExport = {
  format: 'light-media-sites',
  version: 1,
  sites: [{ id: 'fixture-site', name: '馒头', kind: 'mteam', api_key: 'synthetic-credential' }],
};
const file = (value) => ({
  size: 200,
  text: async () => (typeof value === 'string' ? value : JSON.stringify(value)),
});
const result = () => ({
  total: 4,
  imported: 2,
  created: 1,
  updated: 1,
  skipped: 1,
  failed: 1,
  results: [
    { index: 1, name: '馒头', status: 'created', message: '已新增', warnings: [] },
    {
      index: 2,
      name: '憨憨',
      status: 'updated',
      message: '已更新，保留原 ID',
      warnings: ['代理配置未迁移'],
    },
    { index: 3, name: '第3项', status: 'skipped', message: '站点尚未适配', warnings: [] },
    { index: 4, name: 'UBits', status: 'failed', message: '缺少登录凭据', warnings: [] },
  ],
});

test('MoviePilot arrays and local export envelopes pass through unchanged without exposing parse excerpts', async () => {
  assert.deepEqual(await transfer.readSiteImport(file(moviePilot)), moviePilot);
  assert.deepEqual(
    await transfer.readSiteImport(file('\uFEFF' + JSON.stringify(localExport))),
    localExport,
  );
  await assert.rejects(
    transfer.readSiteImport(file('{"cookie":"synthetic-credential" malformed')),
    (error) => {
      assert.match(error.message, /不是有效的 JSON/);
      assert.ok(!error.message.includes('synthetic-credential'));
      return true;
    },
  );
});

test('oversized and unreadable site files never leak file contents through errors', async () => {
  let read = false;
  await assert.rejects(
    transfer.readSiteImport({
      size: 2 * 1024 * 1024 + 1,
      text: async () => {
        read = true;
        return '';
      },
    }),
    /2 MiB/,
  );
  assert.equal(read, false);
  await assert.rejects(
    transfer.readSiteImport({
      size: 20,
      text: async () => {
        throw new Error('synthetic-credential');
      },
    }),
    /无法读取选择的文件/,
  );
});

test('import success requires complete per-site outcomes rather than only an HTTP response', () => {
  assert.deepEqual(transfer.confirmedSiteImport(result()), result());
  for (const malformed of [
    {},
    { ...result(), imported: 4 },
    { ...result(), results: [] },
    { ...result(), failed: -1 },
  ])
    assert.throws(() => transfer.confirmedSiteImport(malformed), /未返回完整导入结果/);
});

function exportEnvironment(t, response) {
  const original = {
    fetch: globalThis.fetch,
    document: globalThis.document,
    location: globalThis.location,
    window: globalThis.window,
    create: URL.createObjectURL,
    revoke: URL.revokeObjectURL,
  };
  const fixture = { requests: [], downloads: [], revoked: [], expired: false };
  globalThis.location = { pathname: '/light-media/' };
  globalThis.fetch = async (path, options) => {
    fixture.requests.push({ path, options });
    return response;
  };
  globalThis.window = {
    dispatchEvent: (event) => {
      fixture.expired = event.type === 'session-expired';
    },
  };
  URL.createObjectURL = (blob) => {
    fixture.blob = blob;
    return 'blob:synthetic-export';
  };
  URL.revokeObjectURL = (url) => fixture.revoked.push(url);
  globalThis.document = {
    body: { appendChild: () => {} },
    createElement: (tag) => {
      assert.equal(tag, 'a');
      return {
        click() {
          fixture.downloads.push({ href: this.href, filename: this.download });
        },
        remove() {
          fixture.removed = true;
        },
      };
    },
  };
  t.after(() => {
    globalThis.fetch = original.fetch;
    globalThis.document = original.document;
    globalThis.location = original.location;
    globalThis.window = original.window;
    URL.createObjectURL = original.create;
    URL.revokeObjectURL = original.revoke;
  });
  return fixture;
}

test('export downloads the authenticated raw attachment with its credentials and cleans the object URL', async (t) => {
  const fixture = exportEnvironment(
    t,
    new Response(JSON.stringify(localExport), {
      headers: {
        'Content-Type': 'application/json',
        'Content-Disposition': 'attachment; filename=light-media-sites-20261015.json',
      },
    }),
  );
  await transfer.downloadSiteExport();
  assert.deepEqual(fixture.requests, [
    {
      path: '/light-media/api/sites/export',
      options: { credentials: 'same-origin', cache: 'no-store' },
    },
  ]);
  assert.deepEqual(JSON.parse(await fixture.blob.text()), localExport);
  assert.deepEqual(fixture.downloads, [
    { href: 'blob:synthetic-export', filename: 'light-media-sites-20261015.json' },
  ]);
  assert.deepEqual(fixture.revoked, ['blob:synthetic-export']);
  assert.ok(fixture.removed);
});

test('authentication and malformed export responses are errors, never downloaded as configuration', async (t) => {
  const fixture = exportEnvironment(
    t,
    new Response(JSON.stringify({ error: { code: 'unauthorized', message: '请重新登录' } }), {
      status: 401,
    }),
  );
  await assert.rejects(transfer.downloadSiteExport(), /请重新登录/);
  assert.equal(fixture.expired, true);
  assert.deepEqual(fixture.downloads, []);
  globalThis.fetch = async () => new Response(JSON.stringify({ data: { sites: [] } }));
  await assert.rejects(transfer.downloadSiteExport(), /未返回有效的站点导出文件/);
  assert.deepEqual(fixture.downloads, []);
});

const source = readFileSync(new URL('../src/components/SiteManager.vue', import.meta.url), 'utf8');
const { descriptor } = parse(source);
const script = compileScript(descriptor, { id: 'site-transfer-test', inlineTemplate: true });
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

async function openSites(t, options = {}) {
  const fixture = { requests: [], downloads: 0, confirms: 0, refreshes: 0, toasts: [] };
  const mounted = [];
  const api = async (path, body) => {
    fixture.requests.push({ path, body });
    await options.beforeRequest?.(path, body, fixture);
    if (path === '/sites/manage') {
      if (
        body === undefined &&
        options.refreshError &&
        fixture.requests.some((request) => request.path === '/sites/import')
      )
        throw new Error('配置读取失败');
      return {
        supported: [],
        sites: [
          {
            config: {
              id: 'fixture-site',
              name: '馒头',
              kind: 'mteam',
              base_url: 'https://api.m-team.cc',
              enabled: false,
            },
            connection: {},
          },
        ],
      };
    }
    if (path === '/sites/import') {
      if (options.importError) throw new Error(options.importError);
      return options.result || result();
    }
    throw new Error('Unexpected request: ' + path);
  };
  const module = { exports: {} };
  new Function('require', 'exports', compiled)((name) => {
    if (name === '../api')
      return {
        api,
        action: (callback) => callback(),
        bytes: String,
        clone: (value) => value,
        date: String,
        friendly: String,
        errorText: (error) => error.message,
        refreshSettings: async () => {
          fixture.refreshes++;
        },
        toast: (message) => fixture.toasts.push(message),
      };
    if (name === '../features/site-transfer')
      return {
        ...transfer,
        downloadSiteExport: async () => {
          fixture.downloads++;
          if (options.exportError) throw new Error(options.exportError);
        },
      };
    if (name === 'vue')
      return {
        ...vue,
        onMounted: (callback) => mounted.push(callback),
        withDirectives: (node) => node,
      };
    throw new Error('Unexpected dependency: ' + name);
  }, module.exports);
  const previousWindow = globalThis.window;
  globalThis.window = {
    confirm: () => {
      fixture.confirms++;
      return options.confirm !== false;
    },
  };
  t.after(() => {
    globalThis.window = previousWindow;
  });
  const scope = vue.effectScope();
  const render = scope.run(() =>
    module.exports.default.setup({}, { expose: () => {}, emit: () => {} }),
  );
  t.after(() => scope.stop());
  for (const callback of mounted) await callback();
  function nodes() {
    const output = [];
    function visit(node) {
      if (node == null) return;
      if (Array.isArray(node)) return node.forEach(visit);
      output.push(node);
      if (typeof node === 'object') visit(node.children);
    }
    visit(render({}, []));
    return output;
  }
  const text = () =>
    nodes()
      .filter((node) => typeof node === 'string')
      .join(' ');
  return {
    fixture,
    nodes,
    text,
    button(label) {
      return nodes().find(
        (node) =>
          node.type === 'button' &&
          typeof node.children === 'string' &&
          node.children.trim() === label,
      );
    },
    async import(value) {
      const input = nodes().find((node) => node.props?.type === 'file');
      const target = { files: value == null ? [] : [file(value)], value: 'chosen.json' };
      await input.props.onChange({ target });
      await vue.nextTick();
      assert.equal(target.value, '', 'the same file remains selectable after import failure');
    },
    async export() {
      const button = nodes().find((node) => node.type === 'button' && node.children === '导出');
      await button.props.onClick();
      await vue.nextTick();
    },
  };
}

test('the actual site manager imports a chosen MoviePilot file and reports partial outcomes without exposing credentials', async (t) => {
  const sites = await openSites(t);
  await sites.import(moviePilot);
  assert.deepEqual(
    sites.fixture.requests.find((request) => request.path === '/sites/import').body,
    moviePilot,
  );
  assert.match(sites.text(), /新增 1，更新 1，跳过 1，失败 1/);
  assert.match(sites.text(), /站点尚未适配/);
  assert.match(sites.text(), /缺少登录凭据/);
  assert.match(sites.text(), /代理配置未迁移/);
  assert.ok(!sites.text().includes('synthetic-credential'));
  assert.equal(sites.fixture.refreshes, 1);
});

test('cancelled or malformed file selection does not send an import and shows only a safe error', async (t) => {
  const sites = await openSites(t);
  await sites.import(null);
  await sites.import('{"cookie":"synthetic-credential" invalid');
  assert.equal(
    sites.fixture.requests.filter((request) => request.path === '/sites/import').length,
    0,
  );
  assert.match(sites.text(), /不是有效的 JSON/);
  assert.ok(!sites.text().includes('synthetic-credential'));
  assert.ok(sites.nodes().some((node) => node.props?.role === 'alert'));
});

test('server import errors remain visible and post-import refresh errors retain the saved result', async (t) => {
  const failed = await openSites(t, { importError: '站点配置不可写' });
  await failed.import(moviePilot);
  assert.match(failed.text(), /站点配置不可写/);
  assert.ok(!failed.text().includes('导入结果：'));
  const refreshed = await openSites(t, { refreshError: true });
  await refreshed.import(moviePilot);
  assert.match(refreshed.text(), /站点已导入，但刷新配置失败/);
  assert.match(refreshed.text(), /新增 1，更新 1，跳过 1，失败 1/);
});

test('export downloads directly on click and surfaces actual download failure', async (t) => {
  const exported = await openSites(t, { confirm: false });
  assert.match(exported.button('导出').props.title, /Cookie \/ API Key/);
  await exported.export();
  assert.equal(exported.fixture.confirms, 0);
  assert.equal(exported.fixture.downloads, 1);
  assert.deepEqual(exported.fixture.toasts, ['站点配置已导出']);
  const failed = await openSites(t, { exportError: '凭据解密失败' });
  await failed.export();
  assert.equal(failed.fixture.confirms, 0);
  assert.equal(failed.fixture.downloads, 1);
  assert.match(failed.text(), /凭据解密失败/);
  assert.equal(failed.fixture.toasts.length, 0);
});

test('an in-flight import blocks old row mutations and stale reloads until refreshed results arrive', async (t) => {
  let release, entered;
  const requested = new Promise((resolve) => {
    entered = resolve;
  });
  const pending = new Promise((resolve) => {
    release = resolve;
  });
  const sites = await openSites(t, {
    beforeRequest: async (path) => {
      if (path === '/sites/import') {
        entered();
        await pending;
      }
    },
  });
  const oldControls = ['启用', '删除', '编辑', '＋ 添加站点', '重新加载', '刷新全部数据'].map(
    (label) => sites.button(label),
  );
  const importing = sites.import(moviePilot);
  await requested;
  for (const label of ['启用', '删除', '编辑', '＋ 添加站点', '重新加载', '刷新全部数据'])
    assert.equal(sites.button(label).props.disabled, true, label);
  const count = sites.fixture.requests.length;
  for (const button of oldControls) await button.props.onClick();
  assert.equal(
    sites.fixture.requests.length,
    count,
    'even stale event handlers cannot post or replace imported settings',
  );
  assert.ok(!sites.text().includes('显示名称'), 'editing cannot open during import');
  release();
  await importing;
  assert.match(sites.text(), /新增 1，更新 1，跳过 1，失败 1/);
  assert.equal(sites.button('启用').props.disabled, false);
});

test('import waits for existing site writes instead of racing their old configuration', async (t) => {
  let release, entered;
  const requested = new Promise((resolve) => {
    entered = resolve;
  });
  const pending = new Promise((resolve) => {
    release = resolve;
  });
  const sites = await openSites(t, {
    beforeRequest: async (path, body) => {
      if (path === '/sites/manage' && body) {
        entered();
        await pending;
      }
    },
  });
  const mutation = sites.button('启用').props.onClick();
  await requested;
  assert.equal(sites.button('导入').props.disabled, true);
  assert.equal(sites.button('导出').props.disabled, true);
  await sites.import(moviePilot);
  assert.equal(
    sites.fixture.requests.filter((request) => request.path === '/sites/import').length,
    0,
  );
  release();
  await mutation;
  assert.equal(sites.button('导入').props.disabled, false);
  await sites.import(moviePilot);
  assert.equal(
    sites.fixture.requests.filter((request) => request.path === '/sites/import').length,
    1,
  );
});
