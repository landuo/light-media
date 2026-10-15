import test from 'node:test';
import assert from 'node:assert/strict';
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

test('MoviePilot-compatible exports download as a top-level array', async (t) => {
  const fixture = exportEnvironment(t, new Response(JSON.stringify(moviePilot)));
  await transfer.downloadSiteExport();
  assert.deepEqual(JSON.parse(await fixture.blob.text()), moviePilot);
});

test('preview accepts both document formats and omits URL credentials and query values', () => {
  assert.deepEqual(transfer.siteImportDocument(moviePilot), {
    format: 'moviepilot',
    sites: moviePilot,
  });
  assert.equal(transfer.siteImportDocument(localExport).format, 'light-media');
  assert.throws(() => transfer.siteImportDocument([]), /没有可导入/);
  assert.throws(() => transfer.siteImportDocument({ sites: moviePilot }), /导出的站点 JSON/);
  assert.deepEqual(
    transfer.sitePreview(
      { name: '示例', url: 'https://user:synthetic-credential@example.org/path?passkey=synthetic' },
      0,
    ),
    { name: '示例', url: 'https://example.org/path', valid: true },
  );
});

test('sequential imports preserve source format and actual progress across partial failures', async () => {
  const body = {
    format: 'light-media-sites',
    version: 1,
    sites: [{ name: 'one' }, { name: 'two' }, { name: 'three' }],
  };
  const requests = [],
    progress = [];
  let inFlight = 0;
  const report = await transfer.importSiteDocument(
    transfer.siteImportDocument(body),
    async (request) => {
      assert.equal(inFlight++, 0);
      requests.push(request);
      await Promise.resolve();
      inFlight--;
      if (requests.length === 2) throw new Error('连接中断，请重新加载核对');
      return {
        total: 1,
        imported: 1,
        created: requests.length === 1 ? 1 : 0,
        updated: requests.length === 3 ? 1 : 0,
        skipped: 0,
        failed: 0,
        results: [
          {
            index: 1,
            name: '示例',
            status: requests.length === 1 ? 'created' : 'updated',
            message: '配置已保存',
            warnings: [],
          },
        ],
      };
    },
    (done, total) => progress.push([done, total]),
  );
  assert.deepEqual(
    requests.map((request) => request.sites.length),
    [1, 1, 1],
  );
  assert.ok(
    requests.every((request) => request.format === 'light-media-sites' && request.version === 1),
  );
  assert.deepEqual(progress, [
    [0, 3],
    [1, 3],
    [2, 3],
    [3, 3],
  ]);
  assert.equal(report.imported, 2);
  assert.equal(report.failed, 1);
  assert.equal(report.results[1].status, 'failed');
  assert.deepEqual(
    report.results.map((row) => row.index),
    [1, 2, 3],
  );
});

test('single-row backend fallback labels retain the original file position without leaking malformed values', async () => {
  const sites = Array.from({ length: 8 }, (_, index) =>
    index < 7
      ? { name: `站点${index + 1}`, url: `https://site${index + 1}.invalid` }
      : { name: 'synthetic-cookie', url: 'malformed-synthetic-cookie' },
  );
  let submitted = 0;
  const report = await transfer.importSiteDocument(
    transfer.siteImportDocument(sites),
    async () => {
      const failed = ++submitted === 8;
      return {
        total: 1,
        imported: failed ? 0 : 1,
        created: failed ? 0 : 1,
        updated: 0,
        skipped: 0,
        failed: failed ? 1 : 0,
        results: [
          {
            index: 1,
            name: failed ? '第 1 项' : '第 1 项',
            status: failed ? 'failed' : 'created',
            message: failed ? '地址无效' : '已保存',
            warnings: [],
          },
        ],
      };
    },
    () => {},
  );
  assert.equal(report.results[7].name, '第 8 项');
  assert.equal(report.results[0].name, '第 1 项', 'actual successful names are not rewritten');
  assert.ok(!JSON.stringify(report).includes('synthetic-cookie'));
  const origin = await transfer.importSiteDocument(
    transfer.siteImportDocument([
      { url: 'https://user:synthetic-cookie@site.invalid/?passkey=synthetic-cookie' },
    ]),
    async () => ({
      total: 1,
      imported: 0,
      created: 0,
      updated: 0,
      skipped: 0,
      failed: 1,
      results: [{ index: 1, name: '第1项', status: 'failed', message: '地址无效', warnings: [] }],
    }),
    () => {},
  );
  assert.equal(origin.results[0].name, 'site.invalid');
  assert.ok(!JSON.stringify(origin).includes('synthetic-cookie'));
});

test('file URLs are invalid previews and the eighth failed row keeps its file position even with an empty backend name', async () => {
  const invalid = { url: 'file:///tmp/synthetic' };
  assert.deepEqual(transfer.sitePreview(invalid, 7), { name: '第 8 项', url: '', valid: false });
  for (const name of ['', '第 1 项']) {
    const sites = [
      ...Array.from({ length: 7 }, (_, index) => ({ url: `https://site${index}.invalid` })),
      invalid,
    ];
    let count = 0;
    const report = await transfer.importSiteDocument(
      transfer.siteImportDocument(sites),
      async () => {
        const failed = ++count === 8;
        return {
          total: 1,
          imported: failed ? 0 : 1,
          created: failed ? 0 : 1,
          updated: 0,
          skipped: 0,
          failed: failed ? 1 : 0,
          results: [
            {
              index: 1,
              name: failed ? name : '示例站点',
              status: failed ? 'failed' : 'created',
              message: failed ? '地址无效' : '已保存',
              warnings: [],
            },
          ],
        };
      },
      () => {},
    );
    assert.equal(report.results[7].name, '第 8 项');
    assert.equal(report.failed, 1);
    assert.equal(report.imported, 7);
    assert.equal(report.results.length, 8);
  }
});
